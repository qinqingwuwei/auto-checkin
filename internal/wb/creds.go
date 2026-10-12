package wb

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
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

const SMSHost = "https://www.workbuddy.cn"
const RefreshURL = "https://copilot.tencent.com/v2/plugin/auth/token/refresh"
const PluginUA = "CLI/2.63.2 CodeBuddy/2.63.2"
const SMSUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.7204.251 Safari/537.36"

var PluginHosts = []string{"https://copilot.tencent.com", "https://www.codebuddy.cn", "https://www.workbuddy.cn"}
var credMu sync.Mutex
var uidPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)

func credPath(uid string) (string, error) {
	if !uidPattern.MatchString(uid) {
		return "", fmt.Errorf("账号 uid 格式无效")
	}
	return filepath.Join(paths.WorkBuddyCreds(), uid+".json"), nil
}
func save(cred util.M) error {
	path, err := credPath(util.String(cred["uid"]))
	if err != nil {
		return err
	}
	if _, ok := cred["savedAt"]; !ok {
		cred["savedAt"] = time.Now().Unix()
	}
	return credstore.Save(path, cred)
}
func Save(cred util.M) error { credMu.Lock(); defer credMu.Unlock(); return save(cred) }
func importLegacy() {
	for _, legacy := range paths.LegacyWorkBuddy() {
		data := util.ReadMap(legacy)
		if !util.Truth(data["accessToken"]) {
			continue
		}
		uid := util.String(util.Or(data["uid"], util.Dig(util.JWTPayload(util.String(data["accessToken"])), "sub", "uid", "user_id")))
		target, err := credPath(uid)
		if err != nil {
			logx.Warn("遗留凭据缺少有效 uid，跳过：%s", legacy)
			continue
		}
		if _, err = os.Stat(target); err == nil {
			continue
		}
		expires := util.JWTExp(util.String(data["accessToken"]))
		if expires == nil && util.Truth(data["expiresIn"]) && util.Truth(data["savedAt"]) {
			expires = int64(util.Float(data["savedAt"]) + util.Float(data["expiresIn"]))
		}
		method := "browser"
		if data["loginMethod"] == "phone" {
			method = "phone"
		}
		cred := util.M{"uid": uid, "accessToken": data["accessToken"], "refreshToken": data["refreshToken"], "expiresAt": expires, "refreshExpiresAt": nil, "loginMethod": method, "phone": data["phone"], "savedAt": util.Or(data["savedAt"], time.Now().Unix()), "source": "legacy:" + filepath.Base(legacy)}
		if err = save(cred); err != nil {
			logx.Warn("遗留凭据导入失败：%s", err)
		} else {
			logx.Info("已从遗留文件导入 WorkBuddy 凭据（uid=%s）：%s", ShortUID(uid), legacy)
		}
	}
}
func LoadAll() []util.M {
	credMu.Lock()
	defer credMu.Unlock()
	out := []util.M{}
	if err := paths.Ensure(); err != nil {
		logx.Warn("凭据目录初始化失败：%s", err)
		return out
	}
	importLegacy()
	files, _ := filepath.Glob(filepath.Join(paths.WorkBuddyCreds(), "*.json"))
	sort.Strings(files)
	for _, path := range files {
		cred := credstore.Load(path)
		if util.Truth(cred["accessToken"]) {
			if _, ok := cred["uid"]; !ok {
				cred["uid"] = filepath.Base(path[:len(path)-5])
			}
			out = append(out, cred)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return util.Float(out[i]["savedAt"]) < util.Float(out[j]["savedAt"]) })
	return out
}
func ShortUID(uid any) string {
	s := []rune(util.String(uid))
	if len(s) == 0 {
		return "-"
	}
	if len(s) > 8 {
		s = s[:8]
	}
	return string(s) + "…"
}
func DisplayName(cred util.M) string {
	claims := util.JWTPayload(util.String(cred["accessToken"]))
	if name := util.String(util.Or(claims["nickname"], claims["preferred_username"])); name != "" {
		return name
	}
	if util.Truth(cred["phone"]) {
		return util.MaskPhone(cred["phone"])
	}
	return ""
}
func CredentialStatus(cred util.M) util.M {
	exp := cred["expiresAt"]
	name := DisplayName(cred)
	var username any
	if name != "" {
		username = name
	}
	return util.M{"uid": cred["uid"], "username": username, "loginMethod": util.Or(cred["loginMethod"], "-"), "phone": util.MaskPhone(cred["phone"]), "source": util.Or(cred["source"], "-"), "expiresAt": exp, "expired": util.Truth(exp) && util.Float(exp) <= util.Now(), "savedAt": cred["savedAt"], "refreshedAt": cred["refreshedAt"]}
}
func refreshWithClient(ctx context.Context, c *netutil.Client, url string, cred util.M) (util.M, error) {
	rt := util.String(cred["refreshToken"])
	if rt == "" {
		return nil, provider.Fail(provider.Auth, "无 refresh token，需重新登录")
	}
	data, status, err := c.Request(ctx, "POST", url, map[string]string{"X-Refresh-Token": rt, "X-Auth-Refresh-Source": "plugin", "Content-Type": "application/json"}, util.M{}, "WorkBuddy 续期")
	if status == 401 || status == 403 {
		return nil, provider.Fail(provider.Auth, "refresh token 已失效，请重新登录：checkin login workbuddy")
	}
	if err != nil {
		return nil, provider.Fail(provider.Auth, err.Error())
	}
	payload := util.Map(data["data"])
	at := util.String(util.Or(payload["accessToken"], payload["access_token"]))
	if status != 200 || util.Float(data["code"]) != 0 || at == "" {
		return nil, provider.Fail(provider.Auth, fmt.Sprintf("续期失败：HTTP %d code=%v msg=%s", status, data["code"], util.String(data["msg"])))
	}
	fresh := util.M{}
	for k, v := range cred {
		fresh[k] = v
	}
	fresh["accessToken"] = at
	fresh["refreshToken"] = util.Or(payload["refreshToken"], payload["refresh_token"], rt)
	exp := util.JWTExp(at)
	if exp == nil {
		if seconds := util.Float(util.Or(payload["expiresIn"], payload["expires_in"])); seconds > 0 {
			exp = time.Now().Unix() + int64(seconds)
		}
	}
	fresh["expiresAt"] = exp
	fresh["refreshedAt"] = time.Now().Unix()
	if err = Save(fresh); err != nil {
		return nil, err
	}
	logx.Info("WorkBuddy（%s）access token 已续期", ShortUID(fresh["uid"]))
	return fresh, nil
}
func ensureWithClient(ctx context.Context, c *netutil.Client, url string, cred util.M, force bool) (util.M, error) {
	now := util.Now()
	if !force && util.Float(cred["expiresAt"]) > now+6*3600 {
		return cred, nil
	}
	if n := util.Float(cred["refreshExpiresAt"]); n > 0 && n <= now {
		return nil, provider.Fail(provider.Auth, "refresh token 已过期，请重新登录：checkin login workbuddy")
	}
	return refreshWithClient(ctx, c, url, cred)
}
func EnsureFresh(ctx context.Context, cred util.M, force bool) (util.M, error) {
	c := netutil.New(30 * time.Second)
	defer c.Close()
	return ensureWithClient(ctx, c, RefreshURL, cred, force)
}
func pluginHeaders() map[string]string {
	return map[string]string{"Accept": "application/json, text/plain, */*", "X-Requested-With": "XMLHttpRequest", "Origin": "https://www.codebuddy.cn", "Referer": "https://www.codebuddy.cn/", "Content-Type": "application/json", "User-Agent": PluginUA}
}
func smsHeaders() map[string]string {
	return map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/plain, */*", "Origin": SMSHost, "Referer": SMSHost + "/", "User-Agent": SMSUA}
}
