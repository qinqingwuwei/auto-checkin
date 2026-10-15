package paths

import (
	"checkin/internal/fileperm"
	"os"
	"path/filepath"
	"runtime"
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
func TraeDevice() string     { return filepath.Join(Credentials(), "trae-device.json") }
func WorkBuddyCreds() string { return filepath.Join(Credentials(), "workbuddy") }
func TraeProfile() string     { return filepath.Join(Root(), "browser", "trae") }
func TraeCookies() string     { return filepath.Join(Root(), "browser", "trae-cookies.json") }

// TraeIDEDevice 指向 Trae CN 客户端本机数据目录里 ahanet 网络栈写的配置，
// 其中 device_id 即客户端发请求时携带的 x-device-id。文件不存在时调用方需回退。
func TraeIDEDevice() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(Home(), "Library", "Application Support", "Trae CN", "ahanet", "tt_net_config.config")
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "Trae CN", "ahanet", "tt_net_config.config")
	default:
		return filepath.Join(Home(), ".config", "Trae CN", "ahanet", "tt_net_config.config")
	}
}
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
		if err := fileperm.Restrict(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}
