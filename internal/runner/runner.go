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
	Key             string   `json:"key"`
	Label           string   `json:"label"`
	Account         string   `json:"account"`
	Success         bool     `json:"success"`
	Already         bool     `json:"already"`
	Skipped         bool     `json:"skipped"`
	Error           bool     `json:"error"`
	AuthExpired     bool     `json:"authExpired"`
	Message         string   `json:"message"`
	CreditToday     *float64 `json:"creditToday"`
	Balance         *float64 `json:"balance"`
	TotalLimit      *float64 `json:"totalLimit"`
	TotalUsed       *float64 `json:"totalUsed"`
	BatchCount      *int     `json:"batchCount"`
	Expiring3d      *float64 `json:"expiring3d"`
	Expiring7d      *float64 `json:"expiring7d"`
	ExpiringCount3d *int     `json:"expiringCount3d"`
	ExpiringCount7d *int     `json:"expiringCount7d"`
	Earliest3d      *float64 `json:"earliest3d"`
	Earliest7d      *float64 `json:"earliest7d"`
	Points          util.M   `json:"-"`
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
	Dispatch    func(context.Context, config.Config, notify.Notification)
}

func New() *Engine {
	return &Engine{Trae: func(context.Context) (provider.Provider, error) { return trae.New(), nil }, WorkBuddy: func(ctx context.Context, c util.M) (provider.Provider, error) { return wb.New(ctx, c) }, Accounts: wb.LoadAll, TraeAccount: trae.Account, Dispatch: notify.Dispatch}
}
func Execute(ctx context.Context, opts Options) (Report, error) { return New().Execute(ctx, opts) }

// Points 实时查询各产品/各账号的积分台账：只读、不领取、不写 state/points，
// 供 WebUI 刷新页面时拿到真实余额（历史快照只在成功执行后追加，会滞后）。
func Points(ctx context.Context) ([]util.M, error) { return New().Points(ctx) }

func (e *Engine) Points(ctx context.Context) ([]util.M, error) {
	cfg, _, err := config.Load()
	if err != nil {
		return nil, err
	}
	rows := []util.M{}
	query := func(key, product, account, label string, factory func() (provider.Provider, error)) {
		row := util.M{"key": key, "product": product, "account": account, "label": label}
		rows = append(rows, row)
		p, err := factory()
		if err != nil {
			row["error"] = logx.Redact(err.Error())
			return
		}
		defer p.Close()
		snapshot, err := p.FetchPoints(ctx)
		if err != nil {
			row["error"] = logx.Redact(err.Error())
			return
		}
		applyRow(row, snapshot, time.Now())
	}
	if cfg.Products.Trae.Enabled {
		query("trae", "trae", "-", "Trae CN", func() (provider.Provider, error) { return e.Trae(ctx) })
	}
	if cfg.Products.WorkBuddy.Enabled {
		for _, cred := range e.Accounts() {
			c := cred
			uid := util.String(util.Or(c["uid"], "unknown"))
			name := wb.DisplayName(c)
			if name == "" {
				name = wb.ShortUID(uid)
			}
			query("workbuddy:"+uid, "workbuddy", uid, "WorkBuddy ("+name+")", func() (provider.Provider, error) { return e.WorkBuddy(ctx, c) })
		}
	}
	return rows, nil
}

// applyRow 把一次实时台账查询的完整积分数据写入返回行：
// 余额、总额度/已用、批次明细、今日实际入账、3 天 / 7 天到期金额与最近到期时间。
func applyRow(row util.M, snapshot provider.Snapshot, now time.Time) {
	summary := expiring.Summarize(snapshot.Batches, float64(now.UnixNano())/1e9)
	row["balance"] = snapshot.Balance
	row["total_limit"] = snapshot.TotalLimit
	row["total_used"] = snapshot.TotalUsed
	row["batch_count"] = summary.BatchCount
	row["credit_today"] = expiring.GrantedOn(snapshot.Batches, now)
	row["expiring_3d"] = summary.Expiring3d
	row["expiring_count_3d"] = summary.ExpiringCount3d
	row["earliest_3d"] = summary.Earliest3d
	row["expiring_7d"] = summary.Expiring7d
	row["expiring_count_7d"] = summary.ExpiringCount7d
	row["earliest_7d"] = summary.Earliest7d
	batches := make([]util.M, 0, len(snapshot.Batches))
	for _, b := range snapshot.Batches {
		batches = append(batches, util.M{"remain": b.Remain, "expire": b.Expire, "limit": b.Limit, "used": b.Used, "created": b.Created, "name": b.Name})
	}
	row["batches"] = batches
}

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
	suffix := ""
	if opts.DryRun {
		suffix = "，dry-run"
	} else if opts.Force {
		suffix = "，--force"
	}
	logx.Info("===== 开始执行（%s%s）=====", report.Date, suffix)
	// Only 同时支持产品名（trae / workbuddy）与单个账号键（trae / workbuddy:<uid>），
	// 供 WebUI 卡片上的「立即签到」只跑某一个账号。
	wants := func(product, key string) bool {
		if len(opts.Only) == 0 {
			return true
		}
		for _, name := range opts.Only {
			if strings.EqualFold(name, product) || strings.EqualFold(name, key) {
				return true
			}
		}
		return false
	}
	if cfg.Products.Trae.Enabled && wants("trae", "trae") {
		item := Item{Key: "trae", Label: "Trae CN", Account: "-"}
		item = e.run(ctx, item, stored, opts, func() (provider.Provider, error) { return e.Trae(ctx) })
		report.Items = append(report.Items, item)
	}
	if cfg.Products.WorkBuddy.Enabled && wants("workbuddy", "workbuddy") {
		accounts := e.Accounts()
		if len(accounts) == 0 {
			report.Items = append(report.Items, Item{Key: "workbuddy", Label: "WorkBuddy", Account: "-", AuthExpired: true, Error: true, Message: "未登录：运行 `checkin login workbuddy --method browser`（微信扫码）或 `--method sms --phone <手机号>`"})
		}
		for _, cred := range accounts {
			uid := util.String(util.Or(cred["uid"], "unknown"))
			if !wants("workbuddy", "workbuddy:"+uid) {
				continue
			}
			name := wb.DisplayName(cred)
			if name == "" {
				name = wb.ShortUID(uid)
			}
			item := Item{Key: "workbuddy:" + uid, Label: "WorkBuddy (" + name + ")", Account: uid}
			item = e.run(ctx, item, stored, opts, func() (provider.Provider, error) { return e.WorkBuddy(ctx, cred) })
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
		// 记录「当天已真实执行过」——成功或失败都算。定时器据此决定是否需要当日补跑：
		// 只要当天执行过就绝不自动重跑，避免限流连击与账号风险。
		stored["lastRunDate"] = report.Date
		if err = state.Save(stored); err != nil {
			return report, fmt.Errorf("状态保存失败：%w", err)
		}
	}
	note := ComposeNotification(report, cfg)
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
func (e *Engine) run(ctx context.Context, item Item, stored util.M, opts Options, factory func() (provider.Provider, error)) Item {
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
				// 失败后一律不自动重跑（含限流 9074）：连续重试会加重上游风控与账号风险，
				// 需要重试时由用户在 WebUI 卡片上点「立即签到」手动触发。
				outcome, claimErr := p.Claim(ctx)
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
				e.fetchPoints(ctx, &item, p, opts.DryRun)
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
			item.Message = "被限流：" + item.Message + "（不再自动重试，可稍后在卡片上点「立即签到」手动重试）"
		}
	}
	logx.Error("%s：%s", item.Label, item.Message)
}
func (e *Engine) fetchPoints(ctx context.Context, item *Item, p provider.Provider, dry bool) {
	snapshot, err := p.FetchPoints(ctx)
	if err != nil {
		item.Error = true
		item.Message += "；积分台账获取失败：" + logx.Redact(err.Error())
		logx.Error("%s 积分台账获取失败：%s", item.Label, err)
		return
	}
	summary := expiring.Summarize(snapshot.Batches, util.Now())
	// 「今日实际入账」按批次创建时间统计当天所有来源（签到、赠送、任务、兑换），
	// 不再只看签到批次，也不用 status 的回显值。
	if granted := expiring.GrantedOn(snapshot.Batches, time.Now()); granted > 0 {
		item.CreditToday = util.Ptr(granted)
	}
	item.Balance = util.Ptr(snapshot.Balance)
	item.TotalLimit = snapshot.TotalLimit
	item.TotalUsed = snapshot.TotalUsed
	item.BatchCount = &summary.BatchCount
	item.Expiring3d = util.Ptr(summary.Expiring3d)
	item.Expiring7d = util.Ptr(summary.Expiring7d)
	item.ExpiringCount3d = &summary.ExpiringCount3d
	item.ExpiringCount7d = &summary.ExpiringCount7d
	item.Earliest3d = summary.Earliest3d
	item.Earliest7d = summary.Earliest7d
	item.Points = util.M{"balance": snapshot.Balance, "total_limit": snapshot.TotalLimit, "total_used": snapshot.TotalUsed, "batch_count": summary.BatchCount, "credit_today": item.CreditToday, "expiring_3d": summary.Expiring3d, "expiring_count_3d": summary.ExpiringCount3d, "earliest_3d": summary.Earliest3d, "expiring_7d": summary.Expiring7d, "expiring_count_7d": summary.ExpiringCount7d, "earliest_7d": summary.Earliest7d}
	logx.Info("%s 积分：余额 %g，批次 %d，3 天内到期 %g（%d 笔），7 天内到期 %g（%d 笔）", item.Label, snapshot.Balance, summary.BatchCount, summary.Expiring3d, summary.ExpiringCount3d, summary.Expiring7d, summary.ExpiringCount7d)
	if dry {
		preview, _ := json.Marshal(item.Points)
		logx.Info("dry-run 快照预览：%s", preview)
	}
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
func ComposeNotification(r Report, c config.Config) *notify.Notification {
	problems, warnings := 0, 0
	for _, i := range r.Items {
		if i.Error {
			problems++
		} else if i.Expiring3d != nil && *i.Expiring3d > 0 {
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
	word := "到期"
	if notification {
		word = "将过期"
	}
	if i.Expiring3d != nil && *i.Expiring3d != 0 {
		bits = append(bits, fmt.Sprintf("3 天内%s %g", word, *i.Expiring3d))
	}
	if i.Expiring7d != nil && *i.Expiring7d != 0 {
		bits = append(bits, fmt.Sprintf("7 天内%s %g", word, *i.Expiring7d))
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
