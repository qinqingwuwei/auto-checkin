package auth

import (
	"checkin/internal/paths"
	"checkin/internal/runner"
	"checkin/internal/trae"
	"checkin/internal/util"
	"checkin/internal/wb"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTraeLogoutRemovesLegacyAndBrowserSessionButKeepsHistory(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	util.WriteJSON(paths.LegacyTrae(), []util.M{{"name": trae.SessionCookie, "value": "fixture-session"}})
	util.WriteJSON(paths.TraeCookies(), util.M{"fixture": "cookie"})
	util.WriteJSON(filepath.Join(paths.TraeProfile(), "Default", "Cookies"), util.M{"fixture": "profile"})
	util.WriteJSON(paths.State(), util.M{"fixture": "history"})
	if len(trae.Load()) == 0 {
		t.Fatal("legacy setup failed")
	}
	if err := Logout("trae", ""); err != nil {
		t.Fatal(err)
	}
	if len(trae.Load()) != 0 {
		t.Fatal("logged-out account was reimported")
	}
	for _, path := range []string{paths.TraeCred(), paths.LegacyTrae(), paths.TraeCookies(), paths.TraeProfile()} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("session remains: " + path)
		}
	}
	if util.ReadMap(paths.State())["fixture"] != "history" {
		t.Fatal("history was removed")
	}
}

func TestWorkBuddyLogoutTargetsOneAccountAndBlocksTraversal(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	wb.Save(util.M{"uid": "account-one", "accessToken": "fixture-one"})
	wb.Save(util.M{"uid": "account-two", "accessToken": "fixture-two"})
	legacy := paths.LegacyWorkBuddy()
	util.WriteJSON(legacy[0], util.M{"uid": "account-one", "accessToken": "fixture-one"})
	util.WriteJSON(legacy[1], util.M{"uid": "account-two", "accessToken": "fixture-two"})
	if err := Logout("workbuddy", "../config"); err == nil {
		t.Fatal("path traversal accepted")
	}
	if err := Logout("workbuddy", "account-one"); err != nil {
		t.Fatal(err)
	}
	accounts := wb.LoadAll()
	if len(accounts) != 1 || accounts[0]["uid"] != "account-two" {
		t.Fatalf("wrong accounts after logout: %+v", accounts)
	}
	if _, err := os.Stat(legacy[0]); !os.IsNotExist(err) {
		t.Fatal("matching legacy credential survived")
	}
	if _, err := os.Stat(legacy[1]); err != nil {
		t.Fatal("another account's legacy credential removed")
	}
}

func TestAccountChangesShareScheduledRunLock(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	paths.Ensure()
	unlock, err := runner.AcquireRunLock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := Logout("trae", ""); err == nil {
		t.Fatal("logout raced an active run")
	}
	if _, err := Login(context.Background(), LoginOptions{Product: "trae", Method: "browser", Timeout: time.Minute}, nil); err == nil {
		t.Fatal("login raced an active run")
	}
}
