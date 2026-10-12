package wb

import (
	"checkin/internal/logx"
	"checkin/internal/netutil"
	"checkin/internal/util"
	"context"
	"fmt"
	"net/url"
	"time"
)

const OAuthWait = 300 * time.Second

func SendSMS(ctx context.Context, phone string) (bool, string) {
	c := netutil.New(20 * time.Second)
	defer c.Close()
	return sendSMSWithClient(ctx, c, SMSHost, phone)
}
func sendSMSWithClient(ctx context.Context, c *netutil.Client, base, phone string) (bool, string) {
	data, status, err := c.Request(ctx, "POST", base+"/v2/plugin/login/send-sms", smsHeaders(), util.M{"phone": phone}, "WorkBuddy 发码")
	if err != nil {
		return false, err.Error()
	}
	if status != 200 || util.Float(data["code"]) != 0 {
		return false, fmt.Sprintf("发码失败：HTTP %d %s", status, util.String(data["msg"]))
	}
	return true, fmt.Sprintf("验证码已发送（%s 秒内有效，手机号 %s）", util.String(util.Or(util.Map(data["data"])["expires_in"], "?")), util.MaskPhone(phone))
}
func LoginSMS(ctx context.Context, phone, code string) (util.M, error) {
	c := netutil.New(30 * time.Second)
	defer c.Close()
	return loginSMSWithClient(ctx, c, SMSHost, phone, code)
}
func loginSMSWithClient(ctx context.Context, c *netutil.Client, base, phone, code string) (util.M, error) {
	data, status, err := c.Request(ctx, "POST", base+"/v2/plugin/login/token", smsHeaders(), util.M{"login_method": "phone", "phone": phone, "sms_code": code}, "WorkBuddy 短信登录")
	if err != nil {
		return nil, err
	}
	if status != 200 || util.Float(data["code"]) != 0 {
		return nil, fmt.Errorf("登录失败：HTTP %d %s", status, util.String(data["msg"]))
	}
	payload := util.Map(data["data"])
	at := util.String(util.Or(payload["accessToken"], payload["access_token"]))
	if at == "" {
		return nil, fmt.Errorf("登录响应缺少 accessToken")
	}
	uid := util.String(util.Dig(util.JWTPayload(at), "sub", "uid", "user_id"))
	if uid == "" {
		return nil, fmt.Errorf("登录响应 token 中未找到 uid（sub）")
	}
	cred := loginCredential(uid, at, util.Or(payload["refreshToken"], payload["refresh_token"]), util.Or(payload["expiresIn"], payload["expires_in"]), "phone", "sms")
	cred["phone"] = phone
	if err = Save(cred); err != nil {
		return nil, err
	}
	logx.Info("WorkBuddy 短信登录成功（uid=%s）", ShortUID(uid))
	return cred, nil
}
func loginCredential(uid, at string, rt, expiresIn any, method, source string) util.M {
	exp := util.JWTExp(at)
	if exp == nil {
		if seconds := util.Float(expiresIn); seconds > 0 {
			exp = time.Now().Unix() + int64(seconds)
		}
	}
	return util.M{"uid": uid, "accessToken": at, "refreshToken": rt, "expiresAt": exp, "refreshExpiresAt": nil, "loginMethod": method, "loginAt": time.Now().Unix(), "source": source}
}
func createState(ctx context.Context, c *netutil.Client, hosts []string) (string, string, string, error) {
	for _, host := range hosts {
		data, status, err := c.Request(ctx, "POST", host+"/v2/plugin/auth/state?platform=CLI", pluginHeaders(), util.M{}, "WorkBuddy OAuth state")
		if err != nil || status != 200 {
			logx.Warn("state 端点 %s 失败（HTTP %d）", host, status)
			continue
		}
		state := util.String(util.Dig(data, "state", "authState", "auth_state"))
		loginURL := util.String(util.Dig(data, "authUrl", "auth_url", "login_url", "loginUrl", "redirect_url"))
		if state != "" {
			return host, state, loginURL, nil
		}
	}
	return "", "", "", fmt.Errorf("获取 OAuth state 失败（三个 host 均不可用），请稍后重试或改用短信登录")
}
func saveOAuth(ctx context.Context, c *netutil.Client, host, state, at string, rt, expiresIn any) (util.M, error) {
	headers := pluginHeaders()
	headers["Authorization"] = "Bearer " + at
	data, status, err := c.Request(ctx, "GET", host+"/v2/plugin/login/account?state="+url.QueryEscape(state), headers, nil, "WorkBuddy OAuth account")
	uid := ""
	if err == nil && status == 200 {
		uid = util.String(util.Dig(data, "uid", "userId", "user_id"))
	}
	if uid == "" {
		uid = util.String(util.Dig(util.JWTPayload(at), "sub", "uid", "user_id"))
	}
	if uid == "" {
		return nil, fmt.Errorf("登录成功但无法确定 uid（account 与 JWT 都没有）")
	}
	cred := loginCredential(uid, at, rt, expiresIn, "browser", "oauth:"+host)
	if err = Save(cred); err != nil {
		return nil, err
	}
	logx.Info("WorkBuddy OAuth 登录成功（uid=%s）", ShortUID(uid))
	return cred, nil
}
func pollOAuth(ctx context.Context, c *netutil.Client, host, state string, interval time.Duration) (util.M, error) {
	lastNote := time.Now()
	for {
		data, status, err := c.Request(ctx, "GET", host+"/v2/plugin/auth/token?state="+url.QueryEscape(state), pluginHeaders(), nil, "WorkBuddy OAuth token")
		if err == nil && status == 200 {
			at := util.String(util.Dig(data, "accessToken", "access_token"))
			if at != "" {
				return saveOAuth(ctx, c, host, state, at, util.Dig(data, "refreshToken", "refresh_token"), util.Dig(data, "expiresIn", "expires_in"))
			}
		}
		if time.Since(lastNote) > 30*time.Second {
			logx.Info("等待 WorkBuddy 登录完成…")
			lastNote = time.Now()
		}
		if err = util.Sleep(ctx, interval); err != nil {
			return nil, err
		}
	}
}
func LoginOAuth(ctx context.Context, timeout time.Duration) (util.M, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("timeout 必须大于 0")
	}
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := netutil.New(20 * time.Second)
	defer c.Close()
	host, state, loginURL, err := createState(wait, c, PluginHosts)
	if err != nil {
		return nil, err
	}
	if loginURL == "" {
		loginURL = host + "/login?platform=CLI&state=" + url.QueryEscape(state)
	}
	u, err := url.Parse(loginURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("OAuth 登录链接无效")
	}
	logx.Info("WorkBuddy 登录页：%s", loginURL)
	launch, cancelLaunch := context.WithTimeout(wait, 20*time.Second)
	opened := util.OpenBrowser(launch, loginURL)
	copied := util.Clipboard(launch, loginURL)
	cancelLaunch()
	logx.Info("已用系统默认浏览器打开 WorkBuddy 登录页（浏览器唤起=%t，链接复制=%t），请在 %d 秒内完成登录。", opened, copied, int(timeout.Seconds()))
	cred, err := pollOAuth(wait, c, host, state, 3*time.Second)
	if err != nil && wait.Err() != nil {
		return nil, fmt.Errorf("OAuth 登录超时或已中断（%d 秒内未完成），请重试或改用短信登录", int(timeout.Seconds()))
	}
	return cred, err
}
