package web

import (
	"checkin/internal/auth"
	"checkin/internal/logx"
	"checkin/internal/trae"
	"checkin/internal/util"
	"checkin/internal/wb"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type AuthOperations struct {
	Login   func(context.Context, auth.LoginOptions, func(string)) (string, error)
	SendSMS func(context.Context, string) (bool, string)
	Logout  func(string, string) error
}

func defaultAuth() AuthOperations {
	return AuthOperations{Login: auth.Login, SendSMS: wb.SendSMS, Logout: auth.Logout}
}

type AccountAction struct {
	Running    bool    `json:"running"`
	Action     string  `json:"action"`
	Product    string  `json:"product"`
	Status     string  `json:"status"`
	Message    string  `json:"message"`
	LoginURL   string  `json:"loginUrl,omitempty"`
	StartedAt  float64 `json:"startedAt,omitempty"`
	FinishedAt float64 `json:"finishedAt,omitempty"`
}

func (s *Server) AuthSnapshot() util.M {
	s.mu.Lock()
	defer s.mu.Unlock()
	retry := util.M{}
	for phone, next := range s.smsRetry {
		retry[phone] = next.Unix()
	}
	return util.M{"operation": s.accountAction, "smsRetry": retry}
}

// smsCooldown 发码成功后的完整冷却；smsInFlight 发送期间占用，避免连点。
// 冷却按手机号独立计算，切到另一个账号的手机号不会被误拦。
const (
	smsCooldown = time.Minute
	smsInFlight = 5 * time.Second
)

func (s *Server) acquireSMS(phone string) (int, string) {
	if next, ok := s.smsRetry[phone]; ok && time.Now().Before(next) {
		return 429, fmt.Sprintf("请等待 %d 秒后再向该手机号发送验证码", int(time.Until(next).Seconds())+1)
	}
	s.smsRetry[phone] = time.Now().Add(smsInFlight)
	return 0, ""
}

func (s *Server) startAuth(action string, opts auth.LoginOptions, uid string) (int, string) {
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return 503, "程序正在退出"
	}
	if s.running || s.accountAction.Running {
		s.mu.Unlock()
		return 409, "已有签到或账号操作正在执行，请稍后重试"
	}
	if action == "sms" {
		if code, msg := s.acquireSMS(opts.Phone); code != 0 {
			s.mu.Unlock()
			return code, msg
		}
	}
	wait, cancel := context.WithTimeout(s.ctx, opts.Timeout)
	s.authCancel = cancel
	message := "正在处理账号操作…"
	if action == "login" && opts.Method == "browser" {
		message = "正在打开登录窗口，请在浏览器中完成登录；可随时取消等待。"
	}
	s.accountAction = AccountAction{Running: true, Action: action, Product: opts.Product, Status: "running", Message: message, StartedAt: util.Now()}
	s.mu.Unlock()
	go func() {
		defer cancel()
		var msg string
		var err error
		switch action {
		case "sms":
			var ok bool
			ok, msg = s.Auth.SendSMS(wait, opts.Phone)
			if !ok {
				err = fmt.Errorf("%s", msg)
			}
		case "logout":
			err = s.Auth.Logout(opts.Product, uid)
			msg = "已清除本工具的登录凭据，积分历史保留。"
		case "login":
			msg, err = s.Auth.Login(wait, opts, func(url string) {
				s.mu.Lock()
				s.accountAction.LoginURL = url
				s.accountAction.Message = "请在 WorkBuddy 授权页面完成登录；窗口未打开时可点击下方链接。"
				s.mu.Unlock()
			})
		}
		status := "success"
		if err != nil {
			status, msg = "error", logx.Redact(err.Error())
			if errors.Is(wait.Err(), context.Canceled) {
				status, msg = "cancelled", "账号操作已取消"
			} else if errors.Is(wait.Err(), context.DeadlineExceeded) {
				msg = "账号操作超时，请重试"
			}
		}
		// Upstream error messages must not echo submitted SMS secrets.
		if opts.Phone != "" {
			msg = strings.ReplaceAll(msg, opts.Phone, util.MaskPhone(opts.Phone))
		}
		if opts.Code != "" {
			msg = strings.ReplaceAll(msg, opts.Code, "[验证码]")
		}
		s.mu.Lock()
		// 发码成功才计完整冷却；失败（含被上游限流）立即释放，避免误拦后续重试。
		if action == "sms" {
			if status == "success" {
				s.smsRetry[opts.Phone] = time.Now().Add(smsCooldown)
			} else {
				delete(s.smsRetry, opts.Phone)
			}
		}
		s.accountAction.Running = false
		s.accountAction.Status = status
		s.accountAction.Message = msg
		s.accountAction.LoginURL = ""
		s.accountAction.FinishedAt = util.Now()
		s.authCancel = nil
		s.mu.Unlock()
	}()
	return 202, message
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request, payload util.M) {
	if r.URL.Path == "/api/auth/cancel" {
		s.mu.Lock()
		if s.authCancel != nil && s.accountAction.Action != "logout" {
			s.authCancel()
			s.accountAction.Message = "正在取消，请等待操作结束…"
		}
		s.mu.Unlock()
		writeJSON(w, 200, s.AuthSnapshot())
		return
	}
	opts := auth.LoginOptions{Product: strings.TrimSpace(util.String(payload["product"])), Method: util.String(payload["method"]), Channel: util.String(payload["channel"]), Phone: strings.TrimSpace(util.String(payload["phone"])), Code: strings.TrimSpace(util.String(payload["code"])), Timeout: wb.OAuthWait}
	if opts.Method == "" {
		opts.Method = "browser"
	}
	if opts.Product == "trae" {
		opts.Timeout = trae.LoginWait
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/auth/")
	uid := util.String(payload["uid"])
	var err error
	switch action {
	case "sms":
		opts.Product, opts.Timeout = "workbuddy", time.Minute
		err = wb.ValidatePhone(opts.Phone)
	case "login":
		err = opts.Validate()
	case "logout":
		err = auth.ValidateLogout(opts.Product, uid)
	}
	if err != nil {
		writeJSON(w, 400, util.M{"error": err.Error()})
		return
	}
	// Trae 签到与设备标识绑定，只允许一个账号：已有可用会话时拒绝再次登录，
	// 避免在同一台机器上换号导致设备标识被重置。会话已过期时仍允许重新登录。
	if action == "login" && opts.Product == "trae" && trae.Locked() {
		writeJSON(w, 409, util.M{"error": "Trae 已是登录状态（签到与设备绑定，仅支持一个账号）：如需更换账号，请先点「登出」再登录。"})
		return
	}
	status, msg := s.startAuth(action, opts, uid)
	writeJSON(w, status, util.M{"started": status == 202, "message": msg, "auth": s.AuthSnapshot()})
}
