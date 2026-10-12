package scheduler

import (
	"checkin/internal/paths"
	"checkin/internal/util"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

func utilJSON(data string, dst any) error { return json.Unmarshal([]byte(data), dst) }

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
func RenderWindowsTask(at, exe, root, userID string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers><CalendarTrigger><StartBoundary>2020-01-01T%s:00</StartBoundary><Enabled>true</Enabled><ScheduleByDay><DaysInterval>1</DaysInterval></ScheduleByDay></CalendarTrigger></Triggers>
  <Principals><Principal id="Author"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><StartWhenAvailable>true</StartWhenAvailable><Enabled>true</Enabled><ExecutionTimeLimit>PT1H</ExecutionTimeLimit></Settings>
  <Actions Context="Author"><Exec><Command>%s</Command><Arguments>--home &quot;%s&quot; run</Arguments><WorkingDirectory>%s</WorkingDirectory></Exec></Actions>
</Task>
`, at, xmlText(userID), xmlText(exe), xmlText(root), xmlText(root))
}
func installWindows(ctx context.Context, at, exe string) (bool, string) {
	current, err := user.Current()
	if err != nil {
		return false, "无法获取当前 Windows 用户：" + err.Error()
	}
	target := filepath.Join(paths.Root(), "bin", "checkin-daily.xml")
	if err := util.AtomicWrite(target, []byte(RenderWindowsTask(at, exe, paths.Root(), current.Username)), 0600); err != nil {
		return false, err.Error()
	}
	defer os.Remove(target)
	output, err := command(ctx, "schtasks", "/Create", "/F", "/TN", WindowsName, "/XML", target)
	if err != nil {
		return false, "schtasks 创建失败：" + output
	}
	return true, fmt.Sprintf("已安装定时任务 %s（每天 %s 执行），日志：%s", WindowsName, at, paths.Log())
}
func uninstallWindows(ctx context.Context) (bool, string) {
	_, err := command(ctx, "schtasks", "/Query", "/TN", WindowsName)
	if err != nil {
		return true, "未安装定时任务"
	}
	output, err := command(ctx, "schtasks", "/Delete", "/TN", WindowsName, "/F")
	if err != nil {
		return false, "schtasks 删除失败：" + output
	}
	return true, "已卸载定时任务 " + WindowsName
}
func windowsStatus(ctx context.Context, info util.M) util.M {
	info["plist"] = WindowsName
	info["log_out"] = paths.Log()
	info["log_err"] = paths.Log()
	output, err := command(ctx, "schtasks", "/Query", "/TN", WindowsName, "/XML")
	if err != nil {
		return info
	}
	info["installed"] = true
	info["loaded"] = true
	var task struct {
		Triggers struct {
			CalendarTrigger struct {
				StartBoundary string `xml:"StartBoundary"`
			} `xml:"CalendarTrigger"`
		} `xml:"Triggers"`
		Actions struct {
			Exec struct {
				Command   string `xml:"Command"`
				Arguments string `xml:"Arguments"`
			} `xml:"Exec"`
		} `xml:"Actions"`
		Settings struct {
			Enabled string `xml:"Enabled"`
		} `xml:"Settings"`
	}
	if err = xml.Unmarshal([]byte(taskXML(output)), &task); err != nil {
		info["error"] = "任务 XML 解析失败"
		info["loaded"] = false
		return info
	}
	if boundary := task.Triggers.CalendarTrigger.StartBoundary; strings.Contains(boundary, "T") && len(strings.SplitN(boundary, "T", 2)[1]) >= 5 {
		info["plist_time"] = strings.SplitN(boundary, "T", 2)[1][:5]
	}
	info["loaded"] = task.Settings.Enabled != "false"
	info["program"] = []string{task.Actions.Exec.Command, task.Actions.Exec.Arguments}
	return info
}
