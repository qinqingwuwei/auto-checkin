package notify

import (
	"checkin/internal/config"
	"checkin/internal/logx"
	"checkin/internal/netutil"
	"checkin/internal/util"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Notification struct {
	Title string
	Lines []string
	Level string
}

func (n Notification) Body() string {
	lines := []string{"### " + n.Title, ""}
	for _, line := range n.Lines {
		lines = append(lines, "- "+line)
	}
	return strings.Join(lines, "\n")
}
func Sign(secret, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "\n" + secret))
	return url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
}
func SignedURL(webhook, secret, timestamp string) string {
	if secret == "" {
		return webhook
	}
	if timestamp == "" {
		timestamp = strconv.FormatInt(time.Now().UnixMilli(), 10)
	}
	sep := "?"
	if strings.Contains(webhook, "?") {
		sep = "&"
	}
	return webhook + sep + "timestamp=" + timestamp + "&sign=" + Sign(secret, timestamp)
}
func SendDingtalk(ctx context.Context, title, text, webhook, secret string) (bool, string) {
	if webhook == "" {
		return false, "未配置钉钉 webhook"
	}
	c := netutil.New(20 * time.Second)
	c.Backoff = 20 * time.Second
	defer c.Close()
	data, status, err := c.Request(ctx, "POST", SignedURL(webhook, secret, ""), nil, util.M{"msgtype": "markdown", "markdown": util.M{"title": title, "text": text}}, "钉钉通知")
	if err != nil {
		return false, err.Error()
	}
	if status != 200 {
		return false, fmt.Sprintf("钉钉返回 HTTP %d", status)
	}
	code := util.Float(data["errcode"])
	if data["errcode"] != nil && code == 0 {
		return true, "ok"
	}
	if code == 310000 {
		return false, "钉钉加签校验失败（310000）：请核对 secret 与时间同步"
	}
	return false, fmt.Sprintf("钉钉发送失败：errcode=%v errmsg=%s", data["errcode"], util.String(data["errmsg"]))
}
func SendLocal(ctx context.Context, title, text string) (bool, string) {
	if runtime.GOOS != "darwin" {
		return false, "当前平台暂不支持本地系统通知（仅日志 + 钉钉）"
	}
	escape := func(s string, max int) string {
		r := []rune(s)
		if len(r) > max {
			r = r[:max]
		}
		s = string(r)
		s = strings.ReplaceAll(s, `\`, `\\`)
		return strings.ReplaceAll(s, `"`, `\"`)
	}
	script := `display notification "` + escape(text, 400) + `" with title "` + escape(title, 120) + `"`
	wait, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := exec.CommandContext(wait, "osascript", "-e", script).Run(); err != nil {
		return false, "osascript 调用失败"
	}
	return true, "ok"
}

// Dispatch is called only by an explicitly invoked checkin run or scheduled run.
func Dispatch(ctx context.Context, cfg config.Config, n Notification) {
	logx.Info("通知[%s] %s\n%s", n.Level, n.Title, strings.Join(n.Lines, "\n"))
	if cfg.Notify.Dingtalk.Enabled {
		if cfg.Notify.Dingtalk.Webhook != "" {
			ok, msg := SendDingtalk(ctx, n.Title, n.Body(), cfg.Notify.Dingtalk.Webhook, cfg.Notify.Dingtalk.Secret)
			logResult("dingtalk", ok, msg)
		} else {
			logx.Warn("钉钉通知未配置 webhook，已跳过（本地通知/日志不受影响）")
		}
	}
	if cfg.Notify.Local.Enabled {
		text := n.Title
		if len(n.Lines) > 0 {
			text = n.Lines[0]
		}
		ok, msg := SendLocal(ctx, n.Title, text)
		logResult("local", ok, msg)
	}
}
func logResult(channel string, ok bool, msg string) {
	if ok {
		logx.Info("通知通道 %s 发送成功", channel)
	} else {
		logx.Warn("通知通道 %s 未送达：%s", channel, msg)
	}
}
