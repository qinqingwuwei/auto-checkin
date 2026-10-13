package auth

import (
	"checkin/internal/runner"
	"checkin/internal/trae"
	"checkin/internal/util"
	"checkin/internal/wb"
	"context"
	"fmt"
	"time"
)

type LoginOptions struct {
	Product, Method, Channel, Phone, Code string
	Timeout                               time.Duration
}

func (o LoginOptions) Validate() error {
	if o.Timeout < time.Second || o.Timeout > 15*time.Minute {
		return fmt.Errorf("登录等待时间应在 1–900 秒之间")
	}
	switch o.Product {
	case "trae":
		if o.Method != "browser" {
			return fmt.Errorf("Trae 仅支持浏览器登录")
		}
		if o.Channel != "" && o.Channel != "chrome" && o.Channel != "msedge" && o.Channel != "chromium" {
			return fmt.Errorf("浏览器应为 chrome / msedge / chromium")
		}
	case "workbuddy":
		if o.Method == "sms" {
			return wb.ValidateSMS(o.Phone, o.Code)
		}
		if o.Method != "browser" {
			return fmt.Errorf("WorkBuddy 登录方式应为 browser / sms")
		}
	default:
		return fmt.Errorf("产品应为 trae / workbuddy")
	}
	return nil
}

// Only user-facing messages leave this package; credentials stay on disk.
func Login(ctx context.Context, o LoginOptions, onURL func(string)) (string, error) {
	if err := o.Validate(); err != nil {
		return "", err
	}
	unlock, err := runner.AcquireRunLock()
	if err != nil {
		return "", err
	}
	defer unlock()
	wait, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	if o.Product == "trae" {
		if _, err := trae.BrowserLogin(wait, o.Channel, o.Timeout); err != nil {
			return "", err
		}
		if _, err := trae.Token(wait, true); err != nil {
			return "", fmt.Errorf("会话已保存，换票验证未通过：%w", err)
		}
		return "Trae 登录成功，换票验证通过", nil
	}
	var cred util.M
	if o.Method == "sms" {
		cred, err = wb.LoginSMS(wait, o.Phone, o.Code)
	} else {
		cred, err = wb.LoginOAuthWithURL(wait, o.Timeout, onURL)
	}
	if err != nil {
		return "", err
	}
	p, err := wb.New(wait, cred)
	if err == nil {
		defer p.Close()
		_, err = p.Status(wait)
	}
	if err != nil {
		return "", fmt.Errorf("凭据已保存，Token 验证未通过（可稍后重试 doctor）：%w", err)
	}
	return "WorkBuddy 登录成功，Token 验证通过（" + wb.ShortUID(cred["uid"]) + "）", nil
}

func ValidateLogout(product, uid string) error {
	if product == "trae" {
		if uid != "" {
			return fmt.Errorf("Trae 登出无需 uid")
		}
		return nil
	}
	if product == "workbuddy" {
		return wb.ValidateUID(uid)
	}
	return fmt.Errorf("产品应为 trae / workbuddy")
}

func Logout(product, uid string) error {
	if err := ValidateLogout(product, uid); err != nil {
		return err
	}
	unlock, err := runner.AcquireRunLock()
	if err != nil {
		return err
	}
	defer unlock()
	if product == "trae" {
		return trae.Logout()
	}
	return wb.Logout(uid)
}
