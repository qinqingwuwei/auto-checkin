package paths

import (
	"os"
	"path/filepath"
	"strings"
)

func Home() string { h, _ := os.UserHomeDir(); return h }
func Root() string {
	if value := os.Getenv("CHECKIN_HOME"); value != "" {
		if value == "~" {
			return Home()
		}
		if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
			value = filepath.Join(Home(), value[2:])
		}
		value, _ = filepath.Abs(value)
		return value
	}
	return filepath.Join(Home(), ".checkin")
}
func Config() string         { return filepath.Join(Root(), "config.json") }
func State() string          { return filepath.Join(Root(), "state.json") }
func Points() string         { return filepath.Join(Root(), "points.jsonl") }
func Logs() string           { return filepath.Join(Root(), "logs") }
func Log() string            { return filepath.Join(Logs(), "checkin.log") }
func Credentials() string    { return filepath.Join(Root(), "credentials") }
func TraeCred() string       { return filepath.Join(Credentials(), "trae.json") }
func WorkBuddyCreds() string { return filepath.Join(Credentials(), "workbuddy") }
func TraeProfile() string    { return filepath.Join(Root(), "browser", "trae") }
func TraeCookies() string    { return filepath.Join(Root(), "browser", "trae-cookies.json") }
func LegacyTrae() string     { return filepath.Join(Root(), "trae-web-state.json") }
func LegacyWorkBuddy() []string {
	return []string{filepath.Join(Root(), "workbuddy-sms.json"), filepath.Join(Root(), "workbuddy-oauth.json")}
}
func Binary() string {
	name := "checkin"
	if filepath.Ext(os.Args[0]) == ".exe" {
		name += ".exe"
	}
	return filepath.Join(Root(), "bin", name)
}
func Ensure() error {
	for _, dir := range []string{Root(), Credentials(), WorkBuddyCreds(), Logs()} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	return os.Chmod(Root(), 0700)
}
