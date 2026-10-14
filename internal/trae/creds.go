package trae

import (
	"checkin/internal/credstore"
	"checkin/internal/logx"
	"checkin/internal/netutil"
	"checkin/internal/paths"
	"checkin/internal/provider"
	"checkin/internal/util"
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

const Base = "https://api.trae.cn"
const SessionCookie = "X-Cloudide-Session"
const TokenPath = "/cloudide/api/v3/common/GetUserToken"
const UserInfoPath = "/cloudide/api/v3/trae/GetUserInfo"
const LoginURL = "https://www.trae.cn/"
const LoginWait = 480 * time.Second

var credMu sync.Mutex

func load() util.M {
	cred := credstore.Load(paths.TraeCred())
	if util.Truth(cred["session"]) {
		return cred
	}
	var cookies []util.M
	if util.ReadJSON(paths.LegacyTrae(), &cookies) != nil {
		return nil
	}
	for _, cookie := range cookies {
		if cookie["name"] != SessionCookie || !util.Truth(cookie["value"]) {
			continue
		}
		var expires any
		if n := util.Float(cookie["expires"]); n > 0 {
			expires = n
		}
		cred = util.M{"session": cookie["value"], "sessionExpires": expires, "jwt": nil, "jwtExp": nil, "loginAt": nil, "savedAt": time.Now().Unix(), "source": "legacy:trae-web-state.json"}
		if err := credstore.Save(paths.TraeCred(), cred); err != nil {
			logx.Warn("Trae 遗留凭据导入失败：%s", err)
			return nil
		}
		logx.Info("已从遗留文件导入 Trae 会话凭据：%s", paths.LegacyTrae())
		return cred
	}
	return nil
}
func Load() util.M { credMu.Lock(); defer credMu.Unlock(); return load() }

// Remove imported credentials and the dedicated browser session too, so a
// later status request or browser login cannot silently restore the account.
func Logout() error {
	credMu.Lock()
	defer credMu.Unlock()
	for _, path := range []string{paths.LegacyTrae(), paths.TraeCookies()} {
		if err := credstore.Remove(path); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(paths.TraeProfile()); err != nil {
		return err
	}
	return credstore.Remove(paths.TraeCred())
}

func credentialRequest(ctx context.Context, c *netutil.Client, base, path, session, label string) (util.M, error) {
	data, status, err := c.Request(ctx, "POST", base+path, map[string]string{SessionCookie: session, "Content-Type": "application/json"}, util.M{}, label)
	if status == 401 || status == 403 {
		return nil, provider.Fail(provider.Auth, "网页会话已失效，请重新登录：checkin login trae")
	}
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("%s HTTP %d", label, status)
	}
	return data, nil
}
func mint(ctx context.Context, c *netutil.Client, base, session string) (string, any, error) {
	data, err := credentialRequest(ctx, c, base, TokenPath, session, "Trae 换票")
	if err != nil {
		return "", nil, err
	}
	token := util.String(util.Map(data["Result"])["Token"])
	if token == "" {
		return "", nil, provider.Fail(provider.Auth, "换票响应缺少 Result.Token（会话可能已失效，请重新登录：checkin login trae）")
	}
	return token, util.JWTExp(token), nil
}
func tokenWithClient(ctx context.Context, c *netutil.Client, base string, force bool) (string, error) {
	credMu.Lock()
	defer credMu.Unlock()
	cred := load()
	if !util.Truth(cred["session"]) {
		return "", provider.Fail(provider.Auth, "未登录：运行 `checkin login trae`")
	}
	now := util.Now()
	if !force && util.Truth(cred["jwt"]) && util.Float(cred["jwtExp"]) > now+600 {
		return util.String(cred["jwt"]), nil
	}
	token, exp, err := mint(ctx, c, base, util.String(cred["session"]))
	if err != nil {
		return "", provider.Fail(provider.Auth, err.Error())
	}
	cred["jwt"] = token
	cred["jwtExp"] = exp
	cred["refreshedAt"] = int64(now)
	if err = credstore.Save(paths.TraeCred(), cred); err != nil {
		return "", err
	}
	logx.Info("Trae JWT 已换发（%s 过期）", util.Timestamp(exp))
	return token, nil
}
func Token(ctx context.Context, force bool) (string, error) {
	c := netutil.New(30 * time.Second)
	defer c.Close()
	return tokenWithClient(ctx, c, Base, force)
}
func CredentialStatus(ctx context.Context) util.M {
	credMu.Lock()
	defer credMu.Unlock()
	cred := load()
	if len(cred) == 0 {
		return util.M{"configured": false, "message": "未登录：运行 `checkin login trae`（复用系统已装浏览器，零下载）"}
	}
	info := util.Map(cred["userInfo"])
	now := util.Now()
	if util.Truth(cred["session"]) && !(len(info) > 0 && now-util.Float(info["fetchedAt"]) < 86400) && now-util.Float(info["failedAt"]) >= 3600 {
		c := netutil.New(8 * time.Second)
		data, err := credentialRequest(ctx, c, Base, UserInfoPath, util.String(cred["session"]), "Trae 用户信息")
		c.Close()
		if err != nil {
			logx.Warn("获取 Trae 用户信息失败（稍后重试）：%s", err)
			info["failedAt"] = int64(now)
		} else {
			r := util.Map(data["Result"])
			info = util.M{"username": util.Or(r["ScreenName"]), "phone": util.Or(r["NonPlainTextMobile"]), "avatar": util.Or(r["AvatarUrl"]), "userId": util.Or(util.String(r["UserID"])), "fetchedAt": int64(now)}
			logx.Info("Trae 用户信息已更新：%s", util.String(util.Or(info["username"], "-")))
		}
		cred["userInfo"] = info
		if err = credstore.Save(paths.TraeCred(), cred); err != nil {
			logx.Warn("Trae 用户信息保存失败：%s", err)
		}
	}
	return util.M{"configured": util.Truth(cred["session"]), "source": util.Or(cred["source"], "-"), "sessionExpires": cred["sessionExpires"], "jwtExp": cred["jwtExp"], "loginAt": cred["loginAt"], "savedAt": cred["savedAt"], "username": info["username"], "phone": info["phone"], "avatar": info["avatar"]}
}
func Account() string {
	id := accountOf(util.String(Load()["jwt"]))
	if id == "" {
		return "-"
	}
	return id
}

// accountOf 从 JWT 的 data 段取账号标识，用于绑定稳定设备标识。
func accountOf(token string) string {
	data := util.Map(util.JWTPayload(token)["data"])
	return util.String(util.Or(data["id"], data["user_id"], data["tid"], ""))
}
