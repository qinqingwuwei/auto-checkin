package wb

import (
	"checkin/internal/netutil"
	"checkin/internal/paths"
	"checkin/internal/util"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSMSAndOAuthRoundTrip(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	token := testToken(time.Now().Unix() + 86400)
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/login/send-sms":
			var m util.M
			json.NewDecoder(r.Body).Decode(&m)
			if m["phone"] != "13812345678" || r.Header.Get("Origin") != SMSHost {
				t.Error("SMS request")
			}
			fmt.Fprint(w, `{"code":0,"data":{"expires_in":300}}`)
		case "/v2/plugin/login/token":
			var m util.M
			json.NewDecoder(r.Body).Decode(&m)
			if m["login_method"] != "phone" || m["sms_code"] != "123456" {
				t.Errorf("login payload %v", m)
			}
			json.NewEncoder(w).Encode(util.M{"code": 0, "data": util.M{"accessToken": token, "refreshToken": "rt"}})
		case "/v2/plugin/auth/state":
			if r.Method != "POST" || r.URL.Query().Get("platform") != "CLI" || r.Header.Get("User-Agent") != PluginUA {
				t.Error("OAuth state request")
			}
			fmt.Fprint(w, `{"data":{"state":"a+b&c","auth_url":"https://example.com/login"}}`)
		case "/v2/plugin/auth/token":
			if r.URL.Query().Get("state") != "a+b&c" {
				t.Error("state escaping")
			}
			polls++
			if polls == 1 {
				fmt.Fprint(w, `{"code":0,"data":{}}`)
			} else {
				json.NewEncoder(w).Encode(util.M{"data": util.M{"access_token": token, "refresh_token": "rt"}})
			}
		case "/v2/plugin/login/account":
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Error("account auth")
			}
			fmt.Fprint(w, `{"data":{"uid":"uid-1"}}`)
		default:
			t.Errorf("unknown path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := netutil.New(time.Second)
	defer c.Close()
	ctx := context.Background()
	if ok, msg := sendSMSWithClient(ctx, c, server.URL, "13812345678"); !ok {
		t.Fatal(msg)
	}
	cred, err := loginSMSWithClient(ctx, c, server.URL, "13812345678", "123456")
	if err != nil || cred["uid"] != "uid-1" || cred["loginMethod"] != "phone" {
		t.Fatalf("SMS %+v %v", cred, err)
	}
	host, state, url, err := createState(ctx, c, []string{server.URL})
	if err != nil || state != "a+b&c" || url != "https://example.com/login" {
		t.Fatalf("state %s %v", state, err)
	}
	wait, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	cred, err = pollOAuth(wait, c, host, state, time.Millisecond)
	if err != nil || polls != 2 || cred["loginMethod"] != "browser" || len(LoadAll()) != 1 {
		t.Fatalf("OAuth %+v %v", cred, err)
	}
}
