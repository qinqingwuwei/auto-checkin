package wb

import (
	"checkin/internal/netutil"
	"checkin/internal/provider"
	"checkin/internal/util"
	"context"
	"fmt"
	"strings"
	"time"
)

var BillingHosts = []string{"https://www.codebuddy.cn", "https://www.workbuddy.cn"}

const StatusPath = "/v2/billing/meter/checkin-activity-status"
const ClaimPath = "/v2/billing/meter/daily-checkin"
const ResourcePath = "/v2/billing/meter/get-user-resource"

type Provider struct {
	Client *netutil.Client
	Cred   util.M
	Hosts  []string
	host   string
}

func New(ctx context.Context, cred util.M) (*Provider, error) {
	c := netutil.New(30 * time.Second)
	fresh, err := ensureWithClient(ctx, c, RefreshURL, cred, false)
	if err != nil {
		c.Close()
		return nil, err
	}
	return &Provider{Client: c, Cred: fresh, Hosts: append([]string{}, BillingHosts...), host: BillingHosts[0]}, nil
}
func (p *Provider) Close() { p.Client.Close() }
func (p *Provider) post(ctx context.Context, path string, body util.M, label string) (util.M, error) {
	headers := map[string]string{"Authorization": "Bearer " + util.String(p.Cred["accessToken"]), "X-User-Id": util.String(util.Or(p.Cred["uid"], "-")), "User-Agent": "WorkBuddy/5.5.6", "Accept": "application/json, text/plain, */*", "Origin": "https://www.codebuddy.cn", "Referer": "https://www.codebuddy.cn/", "Content-Type": "application/json"}
	hosts := []string{}
	if p.host != "" {
		hosts = append(hosts, p.host)
	}
	for _, host := range p.Hosts {
		if host != p.host {
			hosts = append(hosts, host)
		}
	}
	var last error
	for _, host := range hosts {
		data, status, err := p.Client.Request(ctx, "POST", host+path, headers, body, label)
		if status == 401 || status == 403 {
			return nil, provider.Fail(provider.Auth, fmt.Sprintf("%s 鉴权失败（HTTP %d）：请运行 `checkin login workbuddy` 重新登录", label, status))
		}
		if err != nil {
			last = err
			continue
		}
		if status != 200 {
			last = provider.Fail(provider.Other, fmt.Sprintf("%s HTTP %d", label, status))
			continue
		}
		p.host = host
		return data, nil
	}
	if last == nil {
		last = provider.Fail(provider.Other, label+" 全部 host 失败")
	}
	return nil, last
}
func containsAny(msg string, words ...string) bool {
	msg = strings.ToLower(msg)
	for _, w := range words {
		if strings.Contains(msg, w) {
			return true
		}
	}
	return false
}
func checkFailure(data util.M, label string) error {
	if util.Float(data["code"]) == 0 {
		return nil
	}
	msg := util.String(util.Or(data["msg"], data["message"]))
	if containsAny(msg, "未登录", "登录失效", "重新登录", "unauthorized") {
		return provider.Fail(provider.Auth, label+" 失败："+msg+"（请重新登录：checkin login workbuddy）")
	}
	if containsAny(msg, "频繁", "限流", "too many", "rate limit") {
		return provider.Fail(provider.RateLimit, label+" 被限流："+msg)
	}
	return provider.Fail(provider.Other, fmt.Sprintf("%s 失败：code=%v msg=%s", label, data["code"], msg))
}
func (p *Provider) Status(ctx context.Context) (provider.Status, error) {
	data, err := p.post(ctx, StatusPath, util.M{}, "WorkBuddy 签到状态")
	if err != nil {
		return provider.Status{}, err
	}
	if err = checkFailure(data, "WorkBuddy 签到状态"); err != nil {
		return provider.Status{}, err
	}
	payload, ok := data["data"].(map[string]any)
	if !ok {
		payload = data
	}
	checked := util.Truth(util.Dig(payload, "today_checked_in", "checked_in", "todayCheckedIn"))
	msg := util.String(util.Or(data["msg"], data["message"]))
	if msg == "" {
		if checked {
			msg = "今日已签到"
		} else {
			msg = "今日未签到"
		}
	}
	return provider.Status{CheckedIn: checked, CreditsToday: util.NumPtr(util.Dig(payload, "today_credit", "todayCredit", "credit")), StreakDays: util.NumPtr(util.Dig(payload, "streak_days", "streakDays", "continuous_days")), Message: msg}, nil
}
func (p *Provider) Claim(ctx context.Context) (provider.Outcome, error) {
	data, err := p.post(ctx, ClaimPath, util.M{}, "WorkBuddy 签到")
	if err != nil {
		return provider.Outcome{}, err
	}
	msg := util.String(util.Or(data["msg"], data["message"]))
	if util.Float(data["code"]) == 10001 || containsAny(msg, "已签到", "already", "repeat") {
		reason := msg
		if reason == "" {
			reason = util.String(data["code"])
		}
		return provider.Outcome{Success: true, Already: true, Message: "今日已签到（" + reason + "）"}, nil
	}
	if err = checkFailure(data, "WorkBuddy 签到"); err != nil {
		return provider.Outcome{}, err
	}
	if msg == "" {
		msg = "签到成功"
	}
	return provider.Outcome{Success: true, Credits: util.NumPtr(util.Dig(util.Map(data["data"]), "credit", "credits", "credit_amount")), Message: msg}, nil
}
func (p *Provider) FetchPoints(ctx context.Context) (provider.Snapshot, error) {
	data, err := p.post(ctx, ResourcePath, util.M{"PageNumber": 1, "PageSize": 100, "ProductCode": "p_tcaca"}, "WorkBuddy 积分台账")
	if err != nil {
		return provider.Snapshot{}, err
	}
	if err = checkFailure(data, "WorkBuddy 积分台账"); err != nil {
		return provider.Snapshot{}, err
	}
	node := util.Map(data["data"])
	if response, ok := node["Response"].(map[string]any); ok {
		node = response
	}
	if sub, ok := node["Data"].(map[string]any); ok {
		node = sub
	}
	batches := []provider.Batch{}
	for _, v := range util.List(util.Dig(node, "Accounts")) {
		item, ok := v.(map[string]any)
		if !ok {
			continue
		}
		remain, _ := util.Number(util.Or(item["CapacityRemain"], item["Remain"], 0))
		var expire *float64
		for _, key := range []string{"DeductionEndTime", "ExpiredTime", "PackageEndTime", "CycleEndTime"} {
			if n, ok := util.Number(item[key]); ok && n > 1e10 {
				expire = util.Ptr(n / 1000)
				break
			}
		}
		batches = append(batches, provider.Batch{Remain: remain, Expire: expire, Limit: util.NumPtr(item["CapacitySize"]), Used: util.NumPtr(item["CapacityUsed"]), Name: util.String(util.Or(item["PackageName"], item["DealName"]))})
	}
	balance, ok := util.Number(util.Dig(node, "TotalDosage"))
	if !ok {
		for _, b := range batches {
			if b.Remain > 0 {
				balance += b.Remain
			}
		}
	}
	return provider.Snapshot{Balance: util.Round(balance, 2), Batches: batches, Source: ResourcePath}, nil
}
