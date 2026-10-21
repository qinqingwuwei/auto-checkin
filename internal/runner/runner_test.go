package runner

import (
	"checkin/internal/config"
	"checkin/internal/notify"
	"checkin/internal/paths"
	"checkin/internal/provider"
	"checkin/internal/state"
	"checkin/internal/util"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

type fakeProvider struct {
	checked                        bool
	claims, statusCalls, claimRate int
	pointsErr                      error
	batches                        []provider.Batch
}

func (p *fakeProvider) Status(context.Context) (provider.Status, error) {
	p.statusCalls++
	return provider.Status{CheckedIn: p.checked, CreditsToday: util.Ptr(100)}, nil
}
func (p *fakeProvider) Claim(context.Context) (provider.Outcome, error) {
	p.claims++
	if p.claims <= p.claimRate {
		return provider.Outcome{}, provider.Fail(provider.RateLimit, "fixture rate limit")
	}
	return provider.Outcome{Success: true, Credits: util.Ptr(100), Message: "签到成功"}, nil
}
func (p *fakeProvider) FetchPoints(context.Context) (provider.Snapshot, error) {
	if p.pointsErr != nil {
		return provider.Snapshot{}, p.pointsErr
	}
	batches := p.batches
	if batches == nil {
		batches = []provider.Batch{{Remain: 425.5, Expire: util.Ptr(util.Now() + 86400)}}
	}
	return provider.Snapshot{Balance: 425.5, Batches: batches}, nil
}
func (p *fakeProvider) Close() {}
func engine(t *testing.T, p *fakeProvider) *Engine {
	t.Helper()
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	return &Engine{Trae: func(context.Context) (provider.Provider, error) { return p, nil }, WorkBuddy: func(context.Context, util.M) (provider.Provider, error) { return p, nil }, Accounts: func() []util.M { return nil }, TraeAccount: func() string { return "legacy-trae-id" }, Dispatch: func(context.Context, config.Config, notify.Notification) { t.Fatal("dry-run sent a notification") }}
}
func TestDryRunAndAlreadySignedNeverClaim(t *testing.T) {
	p := &fakeProvider{}
	e := engine(t, p)
	initial := util.M{"version": 1, "products": util.M{}, "custom": "preserved"}
	state.Save(initial)
	util.AppendJSONL(paths.Points(), util.M{"ts": 1, "product": "trae"})
	beforeState, _ := os.ReadFile(paths.State())
	beforePoints, _ := os.ReadFile(paths.Points())
	report, err := e.Execute(context.Background(), Options{DryRun: true, Only: []string{"trae"}})
	if err != nil || !report.OK || p.claims != 0 || !report.Items[0].Success {
		t.Fatalf("dry report %+v %v", report, err)
	}
	afterState, _ := os.ReadFile(paths.State())
	afterPoints, _ := os.ReadFile(paths.Points())
	if string(beforeState) != string(afterState) || string(beforePoints) != string(afterPoints) {
		t.Fatal("dry run changed state or points")
	}
	p.checked = true
	e.Dispatch = nil
	report, err = e.Execute(context.Background(), Options{Force: true, Only: []string{"trae"}})
	if err != nil || !report.Items[0].Already || p.claims != 0 {
		t.Fatalf("already claim: %+v %v", report, err)
	}
	if state.Load()["custom"] != "preserved" {
		t.Fatal("unknown state field lost")
	}
	rows := state.History()
	last := rows[len(rows)-1]
	if last["account"] != "legacy-trae-id" || util.Float(last["credit_today"]) != 100 || last["total_limit"] != nil || util.Float(last["expiring_3d"]) != 425.5 {
		t.Fatalf("Python history contract: %+v", last)
	}
}

// 限流失败不再自动重试（避免连击上游风控），并应把当天标记为「已执行」，供定时器判断是否补跑。
func TestRateLimitIsNotRetriedAndMarksDay(t *testing.T) {
	p := &fakeProvider{claimRate: 1}
	e := engine(t, p)
	e.Dispatch = nil
	r, err := e.Execute(context.Background(), Options{Only: []string{"trae"}})
	if err != nil || r.OK || p.claims != 1 {
		t.Fatalf("限流不应自动重试：claims=%d ok=%v err=%v", p.claims, r.OK, err)
	}
	if !r.Items[0].Error || !strings.Contains(r.Items[0].Message, "被限流") || !strings.Contains(r.Items[0].Message, "立即签到") {
		t.Fatalf("限流提示应指向卡片手动重试：%+v", r.Items[0])
	}
	if state.LastRunDate() != util.Today() {
		t.Fatalf("失败当天也应标记已执行：%q", state.LastRunDate())
	}
}
func TestIdempotencyAndLedgerFailure(t *testing.T) {
	p := &fakeProvider{}
	e := engine(t, p)
	e.Dispatch = nil
	r, err := e.Execute(context.Background(), Options{Only: []string{"trae"}})
	if err != nil || !r.OK || p.claims != 1 {
		t.Fatalf("first run %d %v", p.claims, err)
	}
	r, err = e.Execute(context.Background(), Options{Only: []string{"trae"}})
	if err != nil || !r.Items[0].Skipped || p.statusCalls != 1 || p.claims != 1 {
		t.Fatalf("idempotency %+v %v", r, err)
	}
	p.checked = true
	p.pointsErr = fmt.Errorf("fixture ledger failure")
	r, err = e.Execute(context.Background(), Options{Force: true, Only: []string{"trae"}})
	if err != nil || r.OK || !r.Items[0].Success || !r.Items[0].Error {
		t.Fatalf("partial success %+v %v", r, err)
	}
	record := util.Map(util.Map(state.Load()["products"])["trae"])
	if record["successDate"] != util.Today() || record["status"] != "error" {
		t.Fatalf("finalize %+v", record)
	}
}
func TestCreditTodaySumsAllGrantedBatches(t *testing.T) {
	// 「今日实际入账」按批次创建时间统计当天全部来源（签到 150 + 月度赠送 500），
	// 既不取 status 回显，也不只看签到批次。
	today := strings.ReplaceAll(util.Today(), "-", "")
	now := util.Now()
	p := &fakeProvider{checked: true, batches: []provider.Batch{
		{Remain: 100, Limit: util.Ptr(150), Created: util.Ptr(now), Name: "checkin_" + today + "_u1"},
		{Remain: 400, Limit: util.Ptr(500), Created: util.Ptr(now), Name: "monthly_bonus_" + today + "_u1"},
		{Remain: 50, Limit: util.Ptr(50), Created: util.Ptr(now - 86400), Name: "yesterday_bonus"},
	}}
	e := engine(t, p)
	e.Dispatch = nil
	r, err := e.Execute(context.Background(), Options{Only: []string{"trae"}})
	if err != nil || r.Items[0].CreditToday == nil || *r.Items[0].CreditToday != 650 {
		t.Fatalf("ledger-derived creditToday %+v %v", r.Items[0], err)
	}
	record := util.Map(util.Map(state.Load()["products"])["trae"])
	if util.Float(record["creditToday"]) != 650 {
		t.Fatalf("state creditToday %+v", record)
	}
}

func TestMultipleAccountAndLockIsolation(t *testing.T) {
	p := &fakeProvider{checked: true}
	e := engine(t, p)
	e.Dispatch = nil
	e.Accounts = func() []util.M { return []util.M{{"uid": "one"}, {"uid": "two"}} }
	r, err := e.Execute(context.Background(), Options{Only: []string{"workbuddy"}})
	if err != nil || len(r.Items) != 2 || len(util.Map(state.Load()["products"])) != 2 || len(state.History()) != 2 {
		t.Fatalf("multi account %+v %v", r, err)
	}
	release, err := acquireRunLock()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = e.Execute(context.Background(), Options{}); err == nil {
		t.Fatal("second execution got lock")
	}
}

func TestHistoryWriteFailureIsPersisted(t *testing.T) {
	p := &fakeProvider{checked: true}
	e := engine(t, p)
	e.Dispatch = nil
	if err := os.Mkdir(paths.Points(), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := e.Execute(context.Background(), Options{Only: []string{"trae"}})
	if err != nil || r.OK || !r.Items[0].Error {
		t.Fatalf("report %+v %v", r, err)
	}
	record := util.Map(util.Map(state.Load()["products"])["trae"])
	if record["status"] != "error" {
		t.Fatalf("write failure lost: %+v", record)
	}
}
