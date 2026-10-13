package web

import (
	"checkin/internal/auth"
	"checkin/internal/config"
	"checkin/internal/paths"
	"checkin/internal/util"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func accountRequest(t *testing.T, s *Server, path, body string) (int, util.M) {
	t.Helper()
	req := httptest.NewRequest("POST", "http://127.0.0.1"+path, strings.NewReader(body))
	req.Header.Set("X-Checkin", "1")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	var payload util.M
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return w.Code, payload
}

func waitAccount(t *testing.T, s *Server) AccountAction {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		op := s.AuthSnapshot()["operation"].(AccountAction)
		if !op.Running {
			return op
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("account operation did not finish")
	return AccountAction{}
}

func TestBrowserLoginIsAsyncExclusiveAndCancellable(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	s := New(context.Background())
	started := make(chan struct{})
	s.Auth.Login = func(ctx context.Context, opts auth.LoginOptions, onURL func(string)) (string, error) {
		if opts.Product != "workbuddy" {
			t.Error("wrong product")
		}
		onURL("https://www.workbuddy.cn/login?state=fixture-state")
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	if code, _ := accountRequest(t, s, "/api/auth/login", `{"product":"workbuddy"}`); code != 202 {
		t.Fatal(code)
	}
	<-started
	if !s.AuthSnapshot()["operation"].(AccountAction).Running {
		t.Fatal("login not running after HTTP request returned")
	}
	if code, _ := accountRequest(t, s, "/api/auth/login", `{"product":"trae"}`); code != 409 {
		t.Fatal("parallel login allowed")
	}
	if s.start(false, false) {
		t.Fatal("run overlapped login")
	}
	if code, _ := accountRequest(t, s, "/api/auth/cancel", `{}`); code != 200 {
		t.Fatal(code)
	}
	op := waitAccount(t, s)
	if op.Status != "cancelled" || op.LoginURL != "" {
		t.Fatalf("cancel snapshot: %+v", op)
	}
}

func TestSMSValidationCooldownAndSecretRedaction(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	s := New(context.Background())
	calls := 0
	s.Auth.SendSMS = func(ctx context.Context, phone string) (bool, string) {
		calls++
		return true, "验证码已发送到 " + phone
	}
	if code, _ := accountRequest(t, s, "/api/auth/sms", `{"phone":"bad"}`); code != 400 || calls != 0 {
		t.Fatal("invalid phone sent upstream")
	}
	if code, _ := accountRequest(t, s, "/api/auth/sms", `{"phone":"13812345678"}`); code != 202 {
		t.Fatal(code)
	}
	op := waitAccount(t, s)
	if calls != 1 || strings.Contains(op.Message, "13812345678") || op.Status != "success" {
		t.Fatalf("SMS status: %+v", op)
	}
	if code, _ := accountRequest(t, s, "/api/auth/sms", `{"phone":"13812345678"}`); code != 429 {
		t.Fatal("SMS cooldown bypassed")
	}
	s.Auth.Login = func(context.Context, auth.LoginOptions, func(string)) (string, error) {
		return "", fmt.Errorf("phone=13812345678 code=123456 Bearer secret-fixture-token")
	}
	if code, _ := accountRequest(t, s, "/api/auth/login", `{"product":"workbuddy","method":"sms","phone":"13812345678","code":"123456"}`); code != 202 {
		t.Fatal(code)
	}
	op = waitAccount(t, s)
	for _, secret := range []string{"13812345678", "123456", "secret-fixture-token"} {
		if strings.Contains(op.Message, secret) {
			t.Fatal("leaked " + secret)
		}
	}
}

func TestQuitCancelsProcessScheduleAndAccountLogin(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	config.Set("schedule.enabled", true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx)
	s.Stop = cancel
	s.Schedule.Start()
	if s.Schedule.Status()["running"] != true {
		t.Fatal("process schedule did not start")
	}
	started := make(chan struct{})
	s.Auth.Login = func(ctx context.Context, opts auth.LoginOptions, onURL func(string)) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	accountRequest(t, s, "/api/auth/login", `{"product":"trae"}`)
	<-started
	if code, _ := accountRequest(t, s, "/api/quit", `{}`); code != http.StatusOK {
		t.Fatal(code)
	}
	if s.Schedule.Status()["running"] != false || s.start(false, false) {
		t.Fatal("shutdown left schedule or run enabled")
	}
	if op := waitAccount(t, s); op.Status != "cancelled" {
		t.Fatal("shutdown left login running")
	}
}
