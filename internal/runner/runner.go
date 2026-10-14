package runner

import (
	"checkin/internal/config"
	"checkin/internal/expiring"
	"checkin/internal/logx"
	"checkin/internal/notify"
	"checkin/internal/paths"
	"checkin/internal/provider"
	"checkin/internal/state"
	"checkin/internal/trae"
	"checkin/internal/util"
	"checkin/internal/wb"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Item struct {
	Key          string   `json:"key"`
	Label        string   `json:"label"`
	Account      string   `json:"account"`
	Success      bool     `json:"success"`
	Already      bool     `json:"already"`
	Skipped      bool     `json:"skipped"`
	Error        bool     `json:"error"`
	AuthExpired  bool     `json:"authExpired"`
	Message      string   `json:"message"`
	CreditToday  *float64 `json:"creditToday"`
	Balance      *float64 `json:"balance"`
	BatchCount   *int     `json:"batchCount"`
	ExpiringDays *int     `json:"expiringDays"`
	Expiring     *float64 `json:"expiring"`
	Expiring30d  *float64 `json:"expiring30d"`
	Points       util.M   `json:"-"`
}
type Report struct {
	Date       string  `json:"date"`
	DryRun     bool    `json:"dryRun"`
	Items      []Item  `json:"items"`
	StartedAt  float64 `json:"-"`
	FinishedAt float64 `json:"finishedAt"`
	OK         bool    `json:"ok"`
}
type Options struct {
	DryRun, Force bool
	Only          []string
}
type Engine struct {
	Trae        func(context.Context) (provider.Provider, error)
	WorkBuddy   func(context.Context, util.M) (provider.Provider, error)
	Accounts    func() []util.M
	TraeAccount func() string
	RetryWait   time.Duration
	Dispatch    func(context.Context, config.Config, notify.Notification)
}

func New() *Engine {
	return &Engine{Trae: func(context.Context) (provider.Provider, error) { return trae.New(), nil }, WorkBuddy: func(ctx context.Context, c util.M) (provider.Provider, error) { return wb.New(ctx, c) }, Accounts: wb.LoadAll, TraeAccount: trae.Account, RetryWait: 30 * time.Second, Dispatch: notify.Dispatch}
}
func Execute(ctx context.Context, opts Options) (Report, error) { return New().Execute(ctx, opts) }

// AcquireRunLock also serializes account changes with scheduled executions.
func AcquireRunLock() (func(), error) {
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	return acquireRunLock()
}

func (e *Engine) Execute(ctx context.Context, opts Options) (Report, error) {
	report := Report{Date: util.Today(), DryRun: opts.DryRun, StartedAt: util.Now(), OK: true, Items: []Item{}}
	if err := paths.Ensure(); err != nil {
		return report, err
	}
	unlock, err := acquireRunLock()
	if err != nil {
		return report, err
	}
	defer unlock()
	cfg, _, err := config.Load()
	if err != nil {
		return report, err
	}
	stored := state.Load()
	days := cfg.ExpiryWarningDays
	suffix := ""
	if opts.DryRun {
		suffix = "，dry-run"
	} else if opts.Force {
		suffix = "，--force"
	}
	logx.Info("===== 开始执行（%s%s）=====", report.Date, suffix)
	wants := func(product string) bool {
		if len(opts.Only) == 0 {
			return true
		}
		for _, name := range opts.Only {
			if strings.EqualFold(name, product) {
				return true
			}
		}
		return false
	}
	if cfg.Products.Trae.Enabled && wants("trae") {
		item := Item{Key: "trae", Label: "Trae CN", Account: "-"}
		item = e.run(ctx, item, stored, opts, days, func() (provider.Provider, error) { return e.Trae(ctx) })
		report.Items = append(report.Items, item)
	}
	if cfg.Products.WorkBuddy.Enabled && wants("workbuddy") {
		accounts := e.Accounts()
		if len(accounts) == 0 {
			report.Items = append(report.Items, Item{Key: "workbuddy", Label: "WorkBuddy", Account: "-", AuthExpired: true, Error: true, Message: "未登录：运行 `checkin login workbuddy --method browser`（微信扫码）或 `--method sms --phone <手机号>`"})
		}
		for _, cred := range accounts {
			uid := util.String(util.Or(cred["uid"], "unknown"))
			name := wb.DisplayName(cred)
			if name == "" {
				name = wb.ShortUID(uid)
			}
			item := Item{Key: "workbuddy:" + uid, Label: "WorkBuddy (" + name + ")", Account: uid}
			item = e.run(ctx, item, stored, opts, days, func() (provider.Provider, error) { return e.WorkBuddy(ctx, cred) })
			report.Items = append(report.Items, item)
		}
	}
	report.FinishedAt = util.Now()
	for _, item := range report.Items {
		if item.Error {
			report.OK = false
		}
	}
	if !opts.DryRun {
		if err = state.Save(stored); err != nil {
			return report, fmt.Errorf("状态保存失败：%w", err)
		}
	}
	note := ComposeNotification(report, cfg, days)
	if note != nil {
		if opts.DryRun {
			logx.Info("dry-run：以下通知不会真实发送\n%s", strings.Join(note.Lines, "\n"))
		} else if e.Dispatch != nil {
			e.Dispatch(ctx, cfg, *note)
		}
	} else {
		logx.Info("无需通知（无异常、无预警，且每日简报未开启）")
	}
	return report, nil
}
func (e *Engine) run(ctx context.Context, item Item, stored util.M, opts Options, days int, factory func() (provider.Provider, error)) Item {
	record := util.Map(util.Map(stored["products"])[item.Key])
	if !opts.Force && !opts.DryRun && record["successDate"] == util.Today() {
		item.Success = true
		item.Skipped = true
		item.Message = "今日已成功执行，跳过（--force 可强制重跑）"
		logx.Info("%s：%s", item.Label, item.Message)
		return item
	}
	p, err := factory()
	if err != nil {
		markError(&item, err)
	} else {
		defer p.Close()
		status, statusErr := p.Status(ctx)
		if item.Key == "trae" && e.TraeAccount != nil {
			item.Account = e.TraeAccount()
		}
		if statusErr != nil {
			markError(&item, statusErr)
		} else {
			if status.CheckedIn {
				item.Success = true
				item.Already = true
				item.CreditToday = status.CreditsToday
				streak := ""
				if item.Key != "trae" && status.StreakDays != nil && *status.StreakDays != 0 {
					streak = fmt.Sprintf("（连签 %g 天）", *status.StreakDays)
				}
				item.Message = "今日已签到" + streak + "（跳过 claim）"
			} else if opts.DryRun {
				item.Success = true
				item.Message = "dry-run：今日未签到，正式执行时将调用 claim"
			} else {
				outcome, claimErr := p.Claim(ctx)
				var problem *provider.Error
				if errors.As(claimErr, &problem) && problem.Kind == provider.RateLimit {
					logx.Warn("%s 被限流（%s），%d 秒后重试一次", item.Label, problem, int(e.RetryWait.Seconds()))
					if claimErr = util.Sleep(ctx, e.RetryWait); claimErr == nil {
						outcome, claimErr = p.Claim(ctx)
					}
				}
				if claimErr != nil {
					markError(&item, claimErr)
				} else {
					item.Success = outcome.Success
					item.Already = outcome.Already
					item.CreditToday = outcome.Credits
					item.Message = outcome.Message
					if !outcome.Success {
						item.Error = true
					}
				}
			}
			if !item.Error {
				logx.Info("%s 签到：%s", item.Label, item.Message)
				e.fetchPoints(ctx, &item, p, days, opts.DryRun)
			}
		}
	}
	if !opts.DryRun {
		if err = finalize(item, stored); err != nil {
			item.Error = true
			item.Message += "；数据写入失败：" + err.Error()
			record := util.Map(util.Map(stored["products"])[item.Key])
			record["status"] = "error"
			record["message"] = item.Message
			logx.Error("%s", item.Message)
		}
	}
	return item
}
func markError(item *Item, err error) {
	item.Error = true
	item.Message = logx.Redact(err.Error())
	var p *provider.Error
	if errors.As(err, &p) {
		item.AuthExpired = p.Kind == provider.Auth
		if p.Kind == provider.RateLimit {
			item.Message = "被限流：" + item.Message + "（可稍后手动重跑）"
		}
	}
	logx.Error("%s：%s", item.Label, item.Message)
}
func (e *Engine) fetchPoints(ctx context.Context, item *Item, p provider.Provider, days int, dry bool) {
	snapshot, err := p.FetchPoints(ctx)
	if err != nil {
		item.Error = true
		item.Message += "；积分台账获取失败：" + logx.Redact(err.Error())
		logx.Error("%s 积分台账获取失败：%s", item.Label, err)
		return
	}
	summary := expiring.Summarize(snapshot.Batches, util.Now(), days)
	// Trae 的「今日签到所得」以积分台账当天签到批次的额度为准（批次 id 形如 checkin_20261014_<uid>）：
	// status 的 credits/extra_credits 只是回显，并不等于真实入账。
	if item.Key == "trae" {
		if amount := todayCheckinCredits(snapshot.Batches); amount != nil {
			item.CreditToday = amount
		}
	}
	item.Balance = util.Ptr(snapshot.Balance)
	item.BatchCount = &summary.BatchCount
	item.ExpiringDays = &days
	item.Expiring = util.Ptr(summary.ExpiringAmount)
	item.Expiring30d = util.Ptr(summary.Expiring30d)
	item.Points = util.M{"balance": snapshot.Balance, "total_limit": snapshot.TotalLimit, "total_used": snapshot.TotalUsed, "batch_count": summary.BatchCount, "expiring_days": days, "expiring_amount": summary.ExpiringAmount, "expiring_30d": summary.Expiring30d, "earliest_expire": summary.Earliest}
	logx.Info("%s 积分：余额 %g，批次 %d，%d 天内到期 %g，30 天内到期 %g", item.Label, snapshot.Balance, summary.BatchCount, days, summary.ExpiringAmount, summary.Expiring30d)
	if dry {
		preview, _ := json.Marshal(item.Points)
		logx.Info("dry-run 快照预览：%s", preview)
	}
}
// todayCheckinCredits 从积分台账里取当天签到批次的额度，作为「今日签到所得」的准确值。
func todayCheckinCredits(batches []provider.Batch) *float64 {
	prefix := "checkin_" + strings.ReplaceAll(util.Today(), "-", "") + "_"
	for _, b := range batches {
		if b.Limit != nil && strings.HasPrefix(b.Name, prefix) {
			return b.Limit
		}
	}
	return nil
}
func finalize(item Item, stored util.M) error {
	products := util.Map(stored["products"])
	record := util.Map(products[item.Key])
	if item.Success && !item.Skipped {
		record["successDate"] = util.Today()
	}
	status := "ok"
	if item.Error {
		status = "error"
	} else if item.Skipped {
		status = "skip"
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	record["status"] = status
	record["at"] = now
	record["message"] = item.Message
	record["creditToday"] = item.CreditToday
	record["balance"] = item.Balance
	products[item.Key] = record
	stored["products"] = products
	stored["updatedAt"] = now
	if item.Points != nil && item.Success {
		product := "workbuddy"
		if item.Key == "trae" {
			product = "trae"
		}
		row := util.M{"ts": time.Now().Unix(), "date": util.Today(), "product": product, "account": item.Account, "credit_today": item.CreditToday}
		for k, v := range item.Points {
			row[k] = v
		}
		return util.AppendJSONL(paths.Points(), row)
	}
	return nil
}
func ComposeNotification(r Report, c config.Config, days int) *notify.Notification {
	problems, warnings := 0, 0
	for _, i := range r.Items {
		if i.Error {
			problems++
		} else if days > 0 && i.Expiring != nil && *i.Expiring > 0 {
			warnings++
		}
	}
	n := notify.Notification{}
	if problems > 0 {
		n.Title = fmt.Sprintf("签到异常（%d 项）", problems)
		n.Level = "error"
	} else if warnings > 0 {
		n.Title = "积分到期预警"
		n.Level = "warn"
	} else if c.Notify.DailySummary {
		n.Title = "签到成功简报"
		n.Level = "info"
	} else {
		return nil
	}
	for _, i := range r.Items {
		tag := "完成"
		if i.Error {
			tag = "异常"
		} else if i.Skipped {
			tag = "跳过"
		}
		text := fmt.Sprintf("[%s] %s：%s", tag, i.Label, i.Message)
		bits := itemBits(i, true)
		if len(bits) > 0 {
			text += "（" + strings.Join(bits, "，") + "）"
		}
		n.Lines = append(n.Lines, text)
	}
	return &n
}
func itemBits(i Item, notification bool) []string {
	bits := []string{}
	if i.CreditToday != nil && *i.CreditToday != 0 {
		bits = append(bits, fmt.Sprintf("今日 +%g", *i.CreditToday))
	}
	if i.Balance != nil {
		bits = append(bits, fmt.Sprintf("余额 %g", *i.Balance))
	}
	if i.Expiring != nil && *i.Expiring != 0 && i.ExpiringDays != nil {
		word := "到期"
		if notification {
			word = "将过期"
		}
		bits = append(bits, fmt.Sprintf("%d 天内%s %g", *i.ExpiringDays, word, *i.Expiring))
	}
	if notification && i.Expiring30d != nil && *i.Expiring30d != 0 {
		bits = append(bits, fmt.Sprintf("30 天内将到期 %g", *i.Expiring30d))
	}
	return bits
}
func ItemLine(i Item) string {
	tag := "OK"
	if i.Error {
		tag = "FAIL"
	} else if i.Skipped {
		tag = "SKIP"
	}
	message := i.Message
	if message == "" {
		message = "成功"
	}
	suffix := ""
	if bits := itemBits(i, false); len(bits) > 0 {
		suffix = "（" + strings.Join(bits, "，") + "）"
	}
	return fmt.Sprintf("[%s] %s：%s%s", tag, i.Label, message, suffix)
}
