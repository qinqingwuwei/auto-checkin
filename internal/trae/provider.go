package trae

import (
	"checkin/internal/netutil"
	"checkin/internal/provider"
	"checkin/internal/util"
	"context"
	"fmt"
	"strings"
	"time"
)

const StatusPath = "/trae/api/v2/ug/checkin_credits/status"
const ClaimPath = "/trae/api/v2/ug/checkin_credits/claim"
const EntsPath = "/trae/api/v2/pay/user_current_entitlement_list"
const ExpiredPath = "/trae/api/v2/pay/expired_ents"
const BillingPath = "/trae/api/v2/pay/cn_credits_billing_status"

var checkinBody = util.M{"trae_client": "IDE", "req_source": 1, "client_version": "3.4.2"}

const UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"

type Provider struct {
	Client       *netutil.Client
	BaseURL, JWT string
}

func New() *Provider       { return &Provider{Client: netutil.New(30 * time.Second), BaseURL: Base} }
func (p *Provider) Close() { p.Client.Close() }
func (p *Provider) post(ctx context.Context, path string, body util.M, label string) (util.M, error) {
	token := p.JWT
	var err error
	if token == "" {
		token, err = tokenWithClient(ctx, p.Client, p.BaseURL, false)
		if err != nil {
			return nil, err
		}
	}
	headers := map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/plain, */*", "Origin": "https://www.trae.cn", "Referer": "https://www.trae.cn/", "X-User-Region": "cn", "trae_client": "IDE", "User-Agent": UA, "Authorization": "Cloud-IDE-JWT " + token}
	data, status, err := p.Client.Request(ctx, "POST", p.BaseURL+path, headers, body, label)
	if status == 401 || status == 403 {
		return nil, provider.Fail(provider.Auth, fmt.Sprintf("%s 鉴权失败（HTTP %d）：请运行 `checkin login trae`", label, status))
	}
	if status == 429 {
		return nil, provider.Fail(provider.RateLimit, label+" HTTP 429")
	}
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, provider.Fail(provider.Other, fmt.Sprintf("%s HTTP %d", label, status))
	}
	return data, nil
}
func checkCode(data util.M, label string) error {
	code := util.Float(data["code"])
	msg := util.String(util.Or(data["msg"], data["message"]))
	switch code {
	case 0, 9095:
		return nil
	case 12153:
		return provider.Fail(provider.Auth, label+" 票失效（12153），请运行 `checkin login trae`")
	case 9074:
		return provider.Fail(provider.RateLimit, label+" 被限流（9074）：请稍后重试")
	default:
		return provider.Fail(provider.Other, fmt.Sprintf("%s 失败：code=%g msg=%s", label, code, msg))
	}
}
func (p *Provider) Status(ctx context.Context) (provider.Status, error) {
	data, err := p.post(ctx, StatusPath, checkinBody, "Trae 签到状态")
	if err != nil {
		return provider.Status{}, err
	}
	if err = checkCode(data, "Trae 签到状态"); err != nil {
		return provider.Status{}, err
	}
	checked := util.Truth(data["checked_in"]) || util.Truth(data["did_checked_in"]) || util.Float(data["code"]) == 9095
	var credits *float64
	if n, ok := util.Number(data["credits"]); ok {
		credits = util.Ptr(n + util.Float(data["extra_credits"]))
	}
	return provider.Status{CheckedIn: checked, DidCheckedIn: util.Truth(data["did_checked_in"]), CreditsToday: credits, Message: fmt.Sprintf("checked_in=%t enable=%v", checked, data["enable"])}, nil
}
func (p *Provider) Claim(ctx context.Context) (provider.Outcome, error) {
	data, err := p.post(ctx, ClaimPath, checkinBody, "Trae 签到")
	if err != nil {
		return provider.Outcome{}, err
	}
	code := util.Float(data["code"])
	msg := util.String(util.Or(data["msg"], data["message"]))
	if code == 9095 {
		return provider.Outcome{Success: true, Already: true, Message: "今日已签到（9095）"}, nil
	}
	if code == 0 {
		return provider.Outcome{Success: true, Credits: util.NumPtr(data["credits"]), Message: "签到成功"}, nil
	}
	if strings.Contains(msg, "已签") {
		return provider.Outcome{Success: true, Already: true, Message: "今日已签到（" + msg + "）"}, nil
	}
	return provider.Outcome{}, checkCode(data, "Trae 签到")
}
func (p *Provider) FetchPoints(ctx context.Context) (provider.Snapshot, error) {
	data, err := p.post(ctx, EntsPath, util.M{"require_usage": true, "full_data": true}, "Trae 积分台账")
	if err != nil {
		return provider.Snapshot{}, err
	}
	if err = checkCode(data, "Trae 积分台账"); err != nil {
		return provider.Snapshot{}, err
	}
	packs := data["user_entitlement_pack_list"]
	if packs == nil {
		packs = util.Dig(data, "user_entitlement_pack_list", "entitlement_pack_list")
	}
	batches := []provider.Batch{}
	for _, v := range util.List(packs) {
		pack, ok := v.(map[string]any)
		if !ok {
			continue
		}
		base := util.Map(pack["entitlement_base_info"])
		expire := util.NumPtr(pack["expire_time"])
		if expire == nil {
			expire = util.NumPtr(base["end_time"])
		}
		if expire != nil && *expire == 0 {
			expire = nil
		}
		quota := util.Map(util.Or(pack["quota"], base["quota"]))
		limit := util.Float(quota["credits_limit"])
		used := util.Float(util.Map(pack["usage"])["credits_amount"])
		batches = append(batches, provider.Batch{Remain: util.Round(limit-used, 4), Expire: expire, Limit: util.Ptr(limit), Used: util.Ptr(used), Name: util.String(util.Or(pack["entitlement_id"], base["entitlement_id"]))})
	}
	summary := util.Map(data["usage_summary"])
	total := util.NumPtr(summary["total_amount"])
	consumed := util.NumPtr(summary["consumed_amount"])
	balance := 0.0
	if total != nil {
		balance = *total - util.Float(summary["consumed_amount"])
	} else {
		for _, b := range batches {
			if b.Remain > 0 {
				balance += b.Remain
			}
		}
	}
	return provider.Snapshot{Balance: util.Round(balance, 2), TotalLimit: total, TotalUsed: consumed, Batches: batches, Source: p.BaseURL + EntsPath}, nil
}
func (p *Provider) FetchExpired(ctx context.Context) ([]util.M, error) {
	data, err := p.post(ctx, ExpiredPath, util.M{}, "Trae 过期记录")
	if err != nil {
		return nil, err
	}
	if err = checkCode(data, "Trae 过期记录"); err != nil {
		return nil, err
	}
	out := []util.M{}
	for _, v := range util.List(data["expired_ent_list"]) {
		item := util.Map(v)
		expire := util.Or(item["expire_time_ms"], item["expire_time"])
		if util.Float(expire) > 1e10 {
			expire = util.Float(expire) / 1000
		}
		out = append(out, util.M{"name": util.Or(item["name"], "-"), "credits": item["credits_limit"], "expire": expire})
	}
	return out, nil
}
func (p *Provider) BillingEnabled(ctx context.Context) (*bool, error) {
	data, err := p.post(ctx, BillingPath, util.M{}, "Trae billing 状态")
	if err != nil {
		return nil, err
	}
	if err = checkCode(data, "Trae billing 状态"); err != nil {
		return nil, err
	}
	if v, ok := data["enable"].(bool); ok {
		return &v, nil
	}
	return nil, nil
}
