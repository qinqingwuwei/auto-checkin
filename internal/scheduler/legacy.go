package scheduler

import (
	"checkin/internal/paths"
	"checkin/internal/util"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
)

const Label = "com.checkin.daily"
const WindowsName = "checkin-daily"

func PlistPath() string {
	return filepath.Join(paths.Home(), "Library", "LaunchAgents", Label+".plist")
}

// schtasks can emit UTF-16 XML when stdout is redirected to a pipe.
func taskXML(output string) string {
	data := []byte(output)
	if len(data) >= 2 && ((data[0] == 0xff && data[1] == 0xfe) || (data[0] == 0xfe && data[1] == 0xff)) {
		little := data[0] == 0xff
		units := make([]uint16, 0, (len(data)-2)/2)
		for i := 2; i+1 < len(data); i += 2 {
			if little {
				units = append(units, uint16(data[i])|uint16(data[i+1])<<8)
			} else {
				units = append(units, uint16(data[i])<<8|uint16(data[i+1]))
			}
		}
		output = string(utf16.Decode(units))
	}
	for _, encoding := range []string{`encoding="UTF-16"`, `encoding="utf-16"`, `encoding='UTF-16'`, `encoding='utf-16'`} {
		output = strings.ReplaceAll(output, encoding, `encoding="UTF-8"`)
	}
	return strings.TrimSpace(output)
}

func ownsPlist(plist util.M, root string) bool {
	return plist["Label"] == Label && util.Map(plist["EnvironmentVariables"])["CHECKIN_HOME"] == root
}

func ownsWindowsTask(output, root string) (bool, error) {
	var task struct {
		Actions struct {
			Exec struct {
				Arguments string `xml:"Arguments"`
			} `xml:"Exec"`
		} `xml:"Actions"`
	}
	if err := xml.Unmarshal([]byte(taskXML(output)), &task); err != nil {
		return false, err
	}
	return strings.EqualFold(task.Actions.Exec.Arguments, `--home "`+root+`" run`), nil
}

func legacyCommand(ctx context.Context, name string, args ...string) (string, error) {
	wait, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	data, err := exec.CommandContext(wait, name, args...).CombinedOutput()
	return strings.TrimSpace(string(data)), err
}

// Upgrade cleanup removes only this tool's task for the current data directory.
// Failures stop UI startup instead of concealing an old background task.
func RemoveLegacy(ctx context.Context) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		target := PlistPath()
		if _, err := os.Stat(target); os.IsNotExist(err) {
			return "", nil
		} else if err != nil {
			return "", err
		}
		data, err := legacyCommand(ctx, "plutil", "-convert", "json", "-o", "-", "--", target)
		var plist util.M
		if err != nil || json.Unmarshal([]byte(data), &plist) != nil {
			return "", fmt.Errorf("无法检查旧版任务 %s；请先执行 launchctl unload -w 并移除该 plist", target)
		}
		if !ownsPlist(plist, paths.Root()) {
			return "", nil
		}
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		if _, err := legacyCommand(ctx, "launchctl", "bootout", domain, target); err != nil {
			legacyCommand(ctx, "launchctl", "unload", "-w", target)
		}
		if _, err := legacyCommand(ctx, "launchctl", "list", Label); err == nil {
			return "", fmt.Errorf("旧版后台任务仍在加载，请先卸载 %s 后重试", Label)
		}
		if err := os.Remove(target); err != nil {
			return "", err
		}
	case "windows":
		output, err := legacyCommand(ctx, "schtasks", "/Query", "/TN", WindowsName, "/XML")
		if err != nil {
			// 任务不存在时 schtasks 以非零退出，输出的错误文案随系统语言和代码页变化
			// （中文系统是 GBK，与 Go 里的 UTF-8 字面量不匹配），不能靠匹配固定字符串判断。
			// 改用整表列举区分「任务不存在」与「查询本身失败」：任务名是 ASCII，跨语言稳定。
			listing, listErr := legacyCommand(ctx, "schtasks", "/Query", "/FO", "CSV", "/NH")
			// 列举输出在中文系统上是 OEM 代码页，重定向到管道时也可能是 UTF-16：
			// 两者都不影响 ASCII 任务名，只需去掉 UTF-16 会引入的 NUL 字节即可可靠匹配。
			if listErr == nil && !strings.Contains(strings.ReplaceAll(listing, "\x00", ""), WindowsName) {
				return "", nil
			}
			return "", fmt.Errorf("无法检查旧版任务 %s：%s", WindowsName, output)
		}
		owned, err := ownsWindowsTask(output, paths.Root())
		if err != nil {
			return "", fmt.Errorf("无法解析旧版任务 %s，请先从任务计划程序删除后重试", WindowsName)
		}
		if !owned {
			return "", nil
		}
		legacyCommand(ctx, "schtasks", "/End", "/TN", WindowsName)
		if _, err := legacyCommand(ctx, "schtasks", "/Delete", "/TN", WindowsName, "/F"); err != nil {
			return "", fmt.Errorf("无法删除旧版任务 %s：%w", WindowsName, err)
		}
	default:
		return "", nil
	}
	return "已移除旧版系统后台定时任务；定时签到现在随程序进程启停", nil
}
