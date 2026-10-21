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
	"os"
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
	switch runtime.GOOS {
	case "darwin":
		return sendDarwin(ctx, title, text)
	case "windows":
		return sendWindows(ctx, title, text)
	default:
		return false, "当前平台暂不支持本地系统通知（仅日志 + 钉钉）"
	}
}

func sendDarwin(ctx context.Context, title, text string) (bool, string) {
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

// sendWindows 依次尝试两种系统通知通道：
// 1) WinRT Toast（Win10/11 原生通知，需 PowerShell 5.1 与通知中心可用）；
// 2) NotifyIcon 托盘气泡（兼容 Win7/8、通知中心被关闭、Toast 未注册的机器）。
// 标题与正文通过环境变量传入，避免中文与引号在脚本里被转义破坏。
func sendWindows(ctx context.Context, title, text string) (bool, string) {
	toast := strings.Join([]string{
		`$ErrorActionPreference = "Stop"`,
		`[void][Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime]`,
		`$template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)`,
		`$nodes = $template.GetElementsByTagName("text")`,
		`[void]$nodes.Item(0).AppendChild($template.CreateTextNode($env:CHECKIN_NOTIFY_TITLE))`,
		`[void]$nodes.Item(1).AppendChild($template.CreateTextNode($env:CHECKIN_NOTIFY_TEXT))`,
		`$toast = [Windows.UI.Notifications.ToastNotification]::new($template)`,
		`$appId = "{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe"`,
		`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($appId).Show($toast)`,
	}, "\n")
	if out, err := runPowerShell(ctx, toast, title, text); err == nil {
		return true, "ok"
	} else {
		toastErr := strings.TrimSpace(string(out))
		if toastErr == "" {
			toastErr = err.Error()
		}
		balloon := strings.Join([]string{
			`$ErrorActionPreference = "Stop"`,
			`Add-Type -AssemblyName System.Windows.Forms`,
			`Add-Type -AssemblyName System.Drawing`,
			`$icon = New-Object System.Windows.Forms.NotifyIcon`,
			`$icon.Icon = [System.Drawing.SystemIcons]::Information`,
			`$icon.BalloonTipTitle = $env:CHECKIN_NOTIFY_TITLE`,
			`$icon.BalloonTipText = $env:CHECKIN_NOTIFY_TEXT`,
			`$icon.Visible = $true`,
			`$icon.ShowBalloonTip(10000)`,
			`Start-Sleep -Milliseconds 800`,
			`$icon.Dispose()`,
		}, "\n")
		if out, berr := runPowerShell(ctx, balloon, title, text); berr == nil {
			return true, "ok"
		} else {
			balloonErr := strings.TrimSpace(string(out))
			if balloonErr == "" {
				balloonErr = berr.Error()
			}
			return false, "Windows 系统通知失败（Toast：" + clip(toastErr, 160) + "；气泡：" + clip(balloonErr, 160) + "）"
		}
	}
}

// runPowerShell 执行一段 PowerShell 脚本并返回合并输出，便于把真实错误带回日志。
func runPowerShell(ctx context.Context, script, title, text string) ([]byte, error) {
	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(wait, "powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-ExecutionPolicy", "Bypass", "-Sta", "-EncodedCommand", encodeUTF16(script))
	cmd.Env = append(os.Environ(), "CHECKIN_NOTIFY_TITLE="+clip(title, 120), "CHECKIN_NOTIFY_TEXT="+clip(text, 400))
	return cmd.CombinedOutput()
}

func clip(s string, max int) string {
	r := []rune(s)
	if len(r) > max {
		return string(r[:max])
	}
	return s
}

// encodeUTF16 生成 -EncodedCommand 需要的 base64（UTF-16LE），保证非 ASCII 文案不乱码。
func encodeUTF16(script string) string {
	utf16 := make([]byte, 0, len(script)*2)
	for _, r := range script {
		if r > 0xFFFF {
			r = '?'
		}
		utf16 = append(utf16, byte(r), byte(r>>8))
	}
	return base64.StdEncoding.EncodeToString(utf16)
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
