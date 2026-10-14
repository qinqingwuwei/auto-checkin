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
			if body["client_version"] != "3.4.2" || body["trae_client"] != "IDE" || body["req_source"] != float64(1) || body["device_id"] == "" || body["device_id"] == nil {
				t.Errorf("bad checkin payload %v", body)
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
	if err != nil || !s.CheckedIn || !s.DidCheckedIn || s.CreditsToday == nil || *s.CreditsToday != 200 {
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
