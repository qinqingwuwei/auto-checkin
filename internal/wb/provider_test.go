package wb

import (
	"checkin/internal/fileperm"
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

func testToken(exp int64) string {
	return "e30." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"sub":"uid-1","nickname":"fixture","exp":%d}`, exp))) + ".sig"
}
func TestWorkBuddyHostFallbackAndLedger(t *testing.T) {
	badCalls, goodCalls := 0, 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		badCalls++
		w.WriteHeader(404)
		fmt.Fprint(w, `not found`)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		goodCalls++
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fixture" || r.Header.Get("X-User-Id") != "uid-1" || r.Header.Get("User-Agent") != "WorkBuddy/5.5.6" {
			t.Error("business headers")
		}
		switch r.URL.Path {
		case StatusPath:
			fmt.Fprint(w, `{"code":0,"data":{"today_checked_in":true,"today_credit":100,"streak_days":13}}`)
		case ClaimPath:
			fmt.Fprint(w, `{"code":10001,"msg":"已签到"}`)
		case ResourcePath:
			var body util.M
			json.NewDecoder(r.Body).Decode(&body)
			if body["ProductCode"] != "p_tcaca" || body["PageSize"] != float64(100) {
				t.Errorf("resource body %v", body)
			}
			fmt.Fprint(w, `{"code":0,"data":{"Response":{"Data":{"TotalDosage":465.5,"Accounts":[{"CapacityRemain":465.5,"DeductionEndTime":1800000000000,"CapacitySize":500,"CapacityUsed":34.5,"PackageName":"fixture"}]}}}}`)
		default:
			t.Error("unexpected path")
		}
	}))
	defer good.Close()
	p := &Provider{Client: netutil.New(time.Second), Cred: util.M{"uid": "uid-1", "accessToken": "fixture"}, Hosts: []string{bad.URL, good.URL}, host: bad.URL}
	defer p.Close()
	ctx := context.Background()
	status, err := p.Status(ctx)
	if err != nil || !status.CheckedIn || *status.CreditsToday != 100 || *status.StreakDays != 13 {
		t.Fatalf("status %+v %v", status, err)
	}
	claim, err := p.Claim(ctx)
	if err != nil || !claim.Already {
		t.Fatal(err)
	}
	snapshot, err := p.FetchPoints(ctx)
	if err != nil || snapshot.Balance != 465.5 || len(snapshot.Batches) != 1 || *snapshot.Batches[0].Expire != 1800000000 {
		t.Fatalf("snapshot %+v %v", snapshot, err)
	}
	if badCalls != 1 || goodCalls != 3 {
		t.Fatalf("sticky host %d %d", badCalls, goodCalls)
	}
}
func TestWorkBuddyAuthDoesNotSwitchHost(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(401) }))
	defer server.Close()
	p := &Provider{Client: netutil.New(time.Second), Cred: util.M{}, Hosts: []string{server.URL, server.URL + "/other"}, host: server.URL}
	defer p.Close()
	_, err := p.Status(context.Background())
	var e *provider.Error
	if !errors.As(err, &e) || e.Kind != provider.Auth || calls != 1 {
		t.Fatalf("auth fail fast %d %v", calls, err)
	}
}
func TestRefreshAndLegacyMultipleAccounts(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	token := testToken(time.Now().Unix() + 86400)
	util.WriteJSON(paths.LegacyWorkBuddy()[0], util.M{"uid": "uid-1", "accessToken": token, "refreshToken": "legacy-rt", "savedAt": 1, "loginMethod": "phone", "phone": "13812345678"})
	util.WriteJSON(paths.LegacyWorkBuddy()[1], util.M{"uid": "uid-1", "accessToken": "older", "savedAt": 0})
	accounts := LoadAll()
	if len(accounts) != 1 || accounts[0]["accessToken"] != token || accounts[0]["source"] != "legacy:workbuddy-sms.json" {
		t.Fatalf("legacy %+v", accounts)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.Header.Get("X-Refresh-Token") != "legacy-rt" || r.Header.Get("X-Auth-Refresh-Source") != "plugin" {
			t.Error("refresh headers")
		}
		json.NewEncoder(w).Encode(util.M{"code": 0, "data": util.M{"access_token": token, "refresh_token": "rotated"}})
	}))
	defer server.Close()
	c := netutil.New(time.Second)
	defer c.Close()
	cred := accounts[0]
	fresh, err := ensureWithClient(context.Background(), c, server.URL, cred, false)
	if err != nil || calls != 0 || fresh["accessToken"] != token {
		t.Fatal("premature refresh")
	}
	cred["expiresAt"] = time.Now().Unix() + 5*3600
	cred["unknown-field"] = "preserved"
	fresh, err = ensureWithClient(context.Background(), c, server.URL, cred, false)
	if err != nil || calls != 1 || fresh["refreshToken"] != "rotated" || fresh["unknown-field"] != "preserved" {
		t.Fatalf("refresh %+v %v", fresh, err)
	}
	cred["refreshExpiresAt"] = time.Now().Unix() - 1
	if _, err = ensureWithClient(context.Background(), c, server.URL, cred, true); err == nil || calls != 1 {
		t.Fatal("expired refresh token used")
	}
	path, _ := credPath("uid-1")
	if !fileperm.Private(path, 0600) {
		t.Fatal("cred permissions")
	}
	if _, err := credPath("../../state"); err == nil {
		t.Fatal("path traversal accepted")
	}
}
