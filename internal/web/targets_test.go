package web

import (
	"checkin/internal/paths"
	"checkin/internal/runner"
	"checkin/internal/util"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunTargetsValidation(t *testing.T) {
	if targets, err := runTargets(nil); err != nil || len(targets) != 0 {
		t.Fatalf("nil targets %v %v", targets, err)
	}
	ok, err := runTargets([]any{"trae", "workbuddy", "workbuddy:uid-1"})
	if err != nil || len(ok) != 3 {
		t.Fatalf("valid targets %v %v", ok, err)
	}
	for _, bad := range []any{"evil", "workbuddy:a/b", "workbuddy:a\\b", "trae:extra", float64(1)} {
		if _, err := runTargets([]any{bad}); err == nil {
			t.Fatalf("accepted invalid target %v", bad)
		}
	}
}

func TestPointsEndpointRejectsWhileRunning(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	s := New(context.Background())
	release := make(chan struct{})
	started := make(chan struct{})
	s.Execute = func(context.Context, runner.Options) (runner.Report, error) {
		close(started)
		<-release
		return runner.Report{Date: util.Today(), OK: true, Items: []runner.Item{}}, nil
	}
	if !s.start(true, false) {
		t.Fatal("start failed")
	}
	<-started
	server := httptest.NewServer(s)
	defer server.Close()
	get := func(path, body string, csrf bool) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(func() string {
			if body == "" {
				return "GET"
			}
			return "POST"
		}(), server.URL+path, strings.NewReader(body))
		if csrf {
			req.Header.Set("X-Checkin", "1")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(data)
	}
	// 执行进行中时 /api/points 应 409，避免并发打同一个积分接口。
	if code, body := get("/api/points", "", false); code != 409 || !strings.Contains(body, "进行中") {
		t.Fatalf("points while running: %d %s", code, body)
	}
	// only 参数非法必须 400，且不启动执行。
	if code, body := get("/api/run", `{"only":["evil"]}`, true); code != 400 {
		t.Fatalf("invalid only: %d %s", code, body)
	}
	close(release)
}
