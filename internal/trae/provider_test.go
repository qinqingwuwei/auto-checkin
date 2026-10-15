package trae

import (
	"checkin/internal/credstore"
	"checkin/internal/netutil"
	"checkin/internal/paths"
	"checkin/internal/provider"
	"checkin/internal/util"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTraeWireProtocolAndLedger(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Cloud-IDE-JWT test-token" || r.Header.Get("X-User-Region") != "cn" || r.Header.Get("trae_client") != "IDE" || r.Header.Get("X-Device-Id") == "" {
			t.Errorf("bad request method/headers: %s", r.Method)
		}
		var body util.M
		json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case StatusPath, ClaimPath:
			// 签到链路只发 req_source，设备标识走 x-device-id 头（对齐 IDE 的 eb()）。
			if len(body) != 1 || body["req_source"] != float64(1) {
				t.Errorf("checkin payload %v", body)
			}
			if r.URL.Path == StatusPath {
				fmt.Fprint(w, `{"code":0,"did_checked_in":true,"credits":100,"extra_credits":100}`)
			} else {
				fmt.Fprint(w, `{"code":9095}`)
			}
		case EntsPath:
			if body["require_usage"] != true || body["full_data"] != true {
				t.Errorf("ledger payload %v", body)
			}
			fmt.Fprint(w, `{"user_entitlement_pack_list":[{"entitlement_base_info":{"quota":{"credits_limit":400},"end_time":1800000000,"entitlement_id":"pack1"},"usage":{"credits_amount":24.3456}}],"usage_summary":{"total_amount":400,"consumed_amount":24.3456}}`)
		case ExpiredPath:
			fmt.Fprint(w, `{"expired_ent_list":[{"name":"expired","expire_time_ms":1790000000000,"credits_limit":10}]}`)
		case BillingPath:
			fmt.Fprint(w, `{"enable":true}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	p := New()
	p.BaseURL = server.URL
	p.JWT = "test-token"
	defer p.Close()
	ctx := context.Background()
	s, err := p.Status(ctx)
	if err != nil || !s.CheckedIn || !s.DidCheckedIn || s.CreditsToday == nil || *s.CreditsToday != 100 {
		t.Fatalf("status %+v %v", s, err)
	}
	claim, err := p.Claim(ctx)
	if err != nil || !claim.Success || !claim.Already {
		t.Fatalf("claim %+v %v", claim, err)
	}
	snapshot, err := p.FetchPoints(ctx)
	if err != nil || snapshot.Balance != 375.65 || len(snapshot.Batches) != 1 || snapshot.Batches[0].Remain != 375.6544 {
		t.Fatalf("snapshot %+v %v", snapshot, err)
	}
	expired, err := p.FetchExpired(ctx)
	if err != nil || expired[0]["expire"] != float64(1790000000) {
		t.Fatalf("expired %+v %v", expired, err)
	}
	billing, err := p.BillingEnabled(ctx)
	if err != nil || billing == nil || !*billing {
		t.Fatalf("billing %v %v", billing, err)
	}
}
func TestTraeErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		code int
		kind provider.ErrorKind
	}{{9074, provider.RateLimit}, {12153, provider.Auth}, {123, provider.Other}} {
		err := checkCode(util.M{"code": tc.code}, "test")
		var e *provider.Error
		if !errors.As(err, &e) || e.Kind != tc.kind {
			t.Fatalf("code %d: %v", tc.code, err)
		}
	}
}

// 限流文案必须带出上游原始 msg，否则无法区分风控/频控。
func TestTraeRateLimitKeepsUpstreamMessage(t *testing.T) {
	err := checkCode(util.M{"code": 9074, "message": "操作太频繁，请稍后再试"}, "Trae 签到")
	if err == nil || !strings.Contains(err.Error(), "操作太频繁") {
		t.Fatalf("upstream message dropped: %v", err)
	}
}

// 客户端设备标识取自 ahanet 的 tt_net_config.config，读到才能对齐设备身份。
func TestIdeDeviceIDParsing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tt_net_config.config")
	if got := ideDeviceID(path); got != "" {
		t.Fatalf("missing file should yield empty, got %q", got)
	}
	content := "device_id&#*832753379059209@$*tnc_abtest&#*17925215@$*tnc_etag&#*35ca55e3-01d38794@$*"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if got := ideDeviceID(path); got != "832753379059209" {
		t.Fatalf("device id %q", got)
	}
}
func TestMintCacheAndLegacyCompatibility(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	exp := time.Now().Unix() + 28800
	token := "e30." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp))) + ".sig"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != TokenPath || r.Header.Get(SessionCookie) != "session-fixture" {
			t.Errorf("mint request")
		}
		json.NewEncoder(w).Encode(util.M{"Result": util.M{"Token": token}})
	}))
	defer server.Close()
	c := netutil.New(time.Second)
	defer c.Close()
	util.WriteJSON(paths.LegacyTrae(), []util.M{{"name": SessionCookie, "value": "session-fixture", "expires": exp}})
	if cred := Load(); cred["session"] != "session-fixture" || cred["source"] != "legacy:trae-web-state.json" {
		t.Fatalf("legacy %+v", cred)
	}
	got, err := tokenWithClient(context.Background(), c, server.URL, false)
	if err != nil || got != token || calls != 1 {
		t.Fatalf("mint %d %v", calls, err)
	}
	cred := Load()
	cred["custom"] = true
	credstore.Save(paths.TraeCred(), cred)
	got, err = tokenWithClient(context.Background(), c, server.URL, false)
	if err != nil || got != token || calls != 1 || Load()["custom"] != true {
		t.Fatal("cache or unknown field changed")
	}
	tokenWithClient(context.Background(), c, server.URL, true)
	if calls != 2 {
		t.Fatal("force did not mint")
	}
}
