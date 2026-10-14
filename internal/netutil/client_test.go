package netutil

import (
	"checkin/internal/paths"
	"checkin/internal/util"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRetriesAndAuthFailFast(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	for _, status := range []int{503, 429, 401, 403, 400} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(status)
			fmt.Fprint(w, `{"code":1}`)
		}))
		c := New(time.Second)
		c.Backoff = 0
		_, got, err := c.Request(context.Background(), "POST", server.URL, nil, util.M{}, "test")
		c.Close()
		server.Close()
		want := 1
		if status == 503 || status == 429 {
			want = 3
		}
		if err != nil || got != status || calls != want {
			t.Fatalf("status %d: %d calls HTTP %d err %v", status, calls, got, err)
		}
	}
}
func TestCancellationAndBadJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[1,2]`) }))
	defer server.Close()
	c := New(time.Second)
	defer c.Close()
	if _, _, err := c.Request(context.Background(), "GET", server.URL, nil, nil, "test"); err == nil {
		t.Fatal("accepted array")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.Request(ctx, "GET", server.URL, nil, nil, "test"); err == nil {
		t.Fatal("ignored cancellation")
	}
}
