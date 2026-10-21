package config

import (
	"checkin/internal/fileperm"
	"checkin/internal/paths"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolated(t *testing.T) {
	t.Helper()
	t.Setenv("CHECKIN_HOME", filepath.Join(t.TempDir(), "home"))
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
}
func TestValidationAndDefaults(t *testing.T) {
	isolated(t)
	cfg, warnings, err := Load()
	if err != nil || len(warnings) > 0 || cfg.Schedule.Time != "08:00" || cfg.Schedule.Enabled != true {
		t.Fatalf("defaults: %+v %v %v", cfg, warnings, err)
	}
	for _, pair := range [][2]string{{"schedule.time", "8:5"}, {"notify.local.enabled", "关"}} {
		if ok, msg := Set(pair[0], pair[1]); !ok {
			t.Fatal(msg)
		}
	}
	cfg, _, _ = Load()
	if cfg.Schedule.Time != "08:05" || cfg.Notify.Local.Enabled {
		t.Fatalf("normalized config: %+v", cfg)
	}
	before, _ := os.ReadFile(paths.Config())
	for _, pair := range [][2]string{{"schedule.time", "25:00"}, {"notify.local.enabled", "maybe"}, {"notify.dingtalk.webhook", "ftp://invalid"}, {"notify.dingtalk.secret", "short"}, {"nope", "1"}} {
		if ok, _ := Set(pair[0], pair[1]); ok {
			t.Fatalf("accepted invalid: %v", pair)
		}
	}
	after, _ := os.ReadFile(paths.Config())
	if string(before) != string(after) {
		t.Fatal("invalid values changed disk")
	}
}
func TestDirtyDiskAndSecrets(t *testing.T) {
	isolated(t)
	os.WriteFile(paths.Config(), []byte(`{"schedule":{"time":"99:99"},"notify":{"daily_summary":"maybe"}}`), 0600)
	cfg, warnings, err := Load()
	if err != nil || len(warnings) != 2 || cfg.Schedule.Time != "08:00" || cfg.Notify.DailySummary {
		t.Fatalf("fallback %+v %v %v", cfg, warnings, err)
	}
	os.WriteFile(paths.Config(), []byte(`{broken`), 0600)
	_, warnings, err = Load()
	if err != nil || len(warnings) != 1 {
		t.Fatalf("corrupt: %v %v", warnings, err)
	}
	secret := "SEC0123456789abcdef"
	Set("notify.dingtalk.secret", secret)
	Set("notify.dingtalk.webhook", "https://example.com/send?access_token=abcdefghijklmn")
	cfg, _, _ = Load()
	data, _ := json.Marshal(Masked(cfg))
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "abcdefghijklmn") {
		t.Fatal("plaintext secret in view")
	}
	if ready, _ := DingtalkReady(cfg); !ready {
		t.Fatal("dingtalk not ready")
	}
	if !fileperm.Private(paths.Config(), 0600) {
		t.Fatal("config mode")
	}
}
