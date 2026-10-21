package web

import (
	"checkin/internal/config"
	"checkin/internal/logx"
	"checkin/internal/paths"
	"checkin/internal/runner"
	"checkin/internal/state"
	"checkin/internal/util"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLocalAPIAndSecretBoundary(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	config.Set("notify.dingtalk.secret", "SEC-super-secret-fixture")
	config.Set("notify.dingtalk.webhook", "https://example.com/send?access_token=super-private-token")
	util.WriteJSON(paths.TraeCred(), util.M{"session": "private-session", "jwt": "private-jwt", "userInfo": util.M{"username": "fixture", "fetchedAt": time.Now().Unix()}})
	s := New(context.Background())
	server := httptest.NewServer(s)
	defer server.Close()
	request := func(method, path, body, host string, header bool) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if host != "" {
			req.Host = host
		}
		if header {
			req.Header.Set("X-Checkin", "1")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CORS enabled")
		}
		return res.StatusCode, string(data)
	}
	if code, body := request("GET", "/", "", "", false); code != 200 || !strings.Contains(body, "签到台账") || !strings.Contains(body, "X-Checkin") {
		t.Fatalf("index %d", code)
	}
	code, body := request("GET", "/api/status", "", "", false)
	if code != 200 {
		t.Fatal(code)
	}
	for _, secret := range []string{"private-session", "private-jwt", "SEC-super-secret-fixture", "super-private-token"} {
		if strings.Contains(body, secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
	var payload util.M
	json.Unmarshal([]byte(body), &payload)
	for _, key := range []string{"date", "state", "products", "credentials", "points", "history", "config", "secretsSet", "dingtalk", "schedule", "run"} {
		if _, ok := payload[key]; !ok {
			t.Fatal("missing " + key)
		}
	}
	if code, _ = request("GET", "/api/status", "", "evil.example", false); code != 403 {
		t.Fatal("bad Host allowed")
	}
	if code, _ = request("POST", "/api/config", `{"key":"notify.daily_summary","value":true}`, "", false); code != 403 {
		t.Fatal("missing CSRF header allowed")
	}
	if code, _ = request("POST", "/api/config", `{"key":"notify.daily_summary","value":true}`, "", true); code != 200 {
		t.Fatal(code)
	}
	if code, _ = request("POST", "/api/config", `{"key":"notify.daily_summary","value":"abc"}`, "", true); code != 400 {
		t.Fatal(code)
	}
	cfg, _, _ := config.Load()
	if !cfg.Notify.DailySummary {
		t.Fatal("bad value persisted")
	}
	if code, _ = request("POST", "/api/config", strings.Repeat("x", 65537), "", true); code != 400 {
		t.Fatal("body limit")
	}
	if code, _ = request("POST", "/api/config", `[]`, "", true); code != 400 {
		t.Fatal("array accepted")
	}
	if code, _ = request("OPTIONS", "/api/config", "", "", false); code != 405 {
		t.Fatal("preflight accepted")
	}
	if code, _ = request("GET", "/favicon.ico", "", "", false); code != 204 {
		t.Fatal("favicon")
	}
}

func TestTraeLegacyAndJWTIDsShareOneCard(t *testing.T) {
	raw := []util.M{{"product": "trae", "account": "-", "ts": 1, "balance": 100}, {"product": "trae", "account": "9007199254740993", "ts": 2, "balance": 200}, {"product": "workbuddy", "account": "uid-1", "ts": 2, "balance": 300}}
	view := historyView(raw)
	latest := state.Latest(view)
	if len(latest) != 2 || latest[0]["account"] != "-" || latest[0]["balance"] != 200 {
		t.Fatalf("card identity %+v", latest)
	}
	if raw[1]["account"] != "9007199254740993" {
		t.Fatal("raw history was changed")
	}
	if view[2]["account"] != "uid-1" {
		t.Fatal("WorkBuddy account changed")
	}
}
func TestAsyncExecutionAndPortFallback(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	s := New(context.Background())
	release := make(chan struct{})
	started := make(chan struct{})
	s.Execute = func(ctx context.Context, opts runner.Options) (runner.Report, error) {
		logx.Info("fixture execute")
		close(started)
		<-release
		return runner.Report{Date: util.Today(), DryRun: opts.DryRun, OK: true, Items: []runner.Item{}}, nil
	}
	if !s.start(true, false) {
		t.Fatal("start failed")
	}
	<-started
	if s.start(false, true) {
		t.Fatal("concurrent start")
	}
	if snapshot := s.RunSnapshot(); snapshot["running"] != true || snapshot["dryRun"] != true || len(snapshot["lines"].([]string)) != 1 {
		t.Fatalf("live snapshot %+v", snapshot)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for util.Truth(s.RunSnapshot()["running"]) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.RunSnapshot()["report"] == nil {
		t.Fatal("report missing")
	}
	first, port, err := Bind(0)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, next, err := Bind(port)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if next == port || !strings.HasPrefix(second.Addr().String(), "127.0.0.1:") {
		t.Fatal("fallback or bind address")
	}
	for _, host := range []string{"127.0.0.1.evil", "evil.example:8765", "127.0.0.1:8765:evil"} {
		if hostOK(host) {
			t.Fatal(fmt.Sprint("allowed ", host))
		}
	}
}
