package scheduler

import (
	"bytes"
	"checkin/internal/config"
	"checkin/internal/paths"
	"checkin/internal/util"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const Label = "com.checkin.daily"
const WindowsName = "checkin-daily"

var mu sync.Mutex

func PlistPath() string {
	return filepath.Join(paths.Home(), "Library", "LaunchAgents", Label+".plist")
}
func Program() []string {
	exe, err := os.Executable()
	if err != nil {
		exe = paths.Binary()
	}
	if strings.Contains(exe, "go-build") || strings.HasSuffix(exe, ".test") {
		if _, err = os.Stat(paths.Binary()); err == nil {
			exe = paths.Binary()
		}
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return []string{exe, "run"}
}
func xmlText(s string) string { var b bytes.Buffer; xml.EscapeText(&b, []byte(s)); return b.String() }
func RenderPlist(at string, args []string, root string) string {
	var hour, minute int
	fmt.Sscanf(at, "%d:%d", &hour, &minute)
	program := []string{}
	for _, arg := range args {
		program = append(program, "        <string>"+xmlText(arg)+"</string>")
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
    <key>Label</key><string>%s</string>
    <key>ProgramArguments</key><array>
%s
    </array>
    <key>WorkingDirectory</key><string>%s</string>
    <key>StartCalendarInterval</key><dict><key>Hour</key><integer>%d</integer><key>Minute</key><integer>%d</integer></dict>
    <key>RunAtLoad</key><false/>
    <key>StandardOutPath</key><string>%s</string>
    <key>StandardErrorPath</key><string>%s</string>
    <key>EnvironmentVariables</key><dict><key>PATH</key><string>/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string><key>CHECKIN_HOME</key><string>%s</string></dict>
</dict></plist>
`, Label, strings.Join(program, "\n"), xmlText(root), hour, minute, xmlText(filepath.Join(root, "logs", "launchd.out.log")), xmlText(filepath.Join(root, "logs", "launchd.err.log")), xmlText(root))
}
func command(ctx context.Context, name string, args ...string) (string, error) {
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	data, err := exec.CommandContext(wait, name, args...).CombinedOutput()
	return strings.TrimSpace(string(data)), err
}
func Install(ctx context.Context) (bool, string) {
	mu.Lock()
	defer mu.Unlock()
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return false, "当前平台暂不支持内置定时任务，请用 cron 调用 checkin run"
	}
	if err := paths.Ensure(); err != nil {
		return false, err.Error()
	}
	cfg, _, err := config.Load()
	if err != nil {
		return false, err.Error()
	}
	args := Program()
	if strings.Contains(args[0], "go-build") || strings.HasSuffix(args[0], ".test") {
		return false, "请先 go build 并安装固定路径的二进制，再运行 schedule install"
	}
	if runtime.GOOS == "windows" {
		return installWindows(ctx, cfg.Schedule.Time, args[0])
	}
	target := PlistPath()
	if err = util.AtomicWrite(target, []byte(RenderPlist(cfg.Schedule.Time, args, paths.Root())), 0644); err != nil {
		return false, err.Error()
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	command(ctx, "launchctl", "bootout", domain, target)
	output, err := command(ctx, "launchctl", "bootstrap", domain, target)
	if err != nil {
		legacy, legacyErr := command(ctx, "launchctl", "load", "-w", target)
		if legacyErr != nil {
			return false, "launchctl bootstrap 失败：" + output + "；load -w 亦失败：" + legacy
		}
	}
	return true, fmt.Sprintf("已安装定时任务 %s（每天 %s 执行），plist：%s", Label, cfg.Schedule.Time, target)
}
func Uninstall(ctx context.Context) (bool, string) {
	mu.Lock()
	defer mu.Unlock()
	if runtime.GOOS == "windows" {
		return uninstallWindows(ctx)
	}
	if runtime.GOOS != "darwin" {
		return false, "当前平台暂不支持内置定时任务"
	}
	target := PlistPath()
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return true, "未安装定时任务（plist 不存在）"
	}
	command(ctx, "launchctl", "bootout", fmt.Sprintf("gui/%d", os.Getuid()), target)
	command(ctx, "launchctl", "unload", "-w", target)
	if err := os.Remove(target); err != nil {
		return false, "删除 plist 失败：" + err.Error()
	}
	return true, "已卸载定时任务 " + Label
}
func Status(ctx context.Context) util.M {
	mu.Lock()
	defer mu.Unlock()
	cfg, _, _ := config.Load()
	args := Program()
	info := util.M{"installed": false, "plist": PlistPath(), "config_time": cfg.Schedule.Time, "plist_time": nil, "loaded": false, "program": args, "log_out": filepath.Join(paths.Logs(), "launchd.out.log"), "log_err": filepath.Join(paths.Logs(), "launchd.err.log")}
	if runtime.GOOS == "windows" {
		return windowsStatus(ctx, info)
	}
	if runtime.GOOS != "darwin" {
		return info
	}
	_, err := os.Stat(PlistPath())
	if err != nil {
		return info
	}
	info["installed"] = true
	data, err := command(ctx, "plutil", "-convert", "json", "-o", "-", "--", PlistPath())
	var plist util.M
	if err != nil {
		info["error"] = "plist 解析失败：" + data
	} else if utilJSON(data, &plist) == nil {
		interval := util.Map(plist["StartCalendarInterval"])
		info["plist_time"] = fmt.Sprintf("%02d:%02d", int(util.Float(interval["Hour"])), int(util.Float(interval["Minute"])))
		if program, ok := plist["ProgramArguments"].([]any); ok {
			info["program"] = program
		}
		info["run_at_load"] = util.Truth(plist["RunAtLoad"])
	}
	output, err := command(ctx, "launchctl", "list", Label)
	info["loaded"] = err == nil
	if len(output) > 200 {
		output = output[:200]
	}
	info["launchctl_output"] = output
	return info
}
