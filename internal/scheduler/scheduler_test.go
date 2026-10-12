package scheduler

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"unicode/utf16"
)

func validXML(t *testing.T, text string) {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(text))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestWindowsTaskUTF16(t *testing.T) {
	text := `<?xml version="1.0" encoding="UTF-16"?><Task><name>中文任务</name></Task>`
	units := utf16.Encode([]rune(text))
	data := []byte{0xff, 0xfe}
	for _, unit := range units {
		data = append(data, byte(unit), byte(unit>>8))
	}
	normalized := taskXML(string(data))
	validXML(t, normalized)
	if !strings.Contains(normalized, "中文任务") {
		t.Fatal("UTF-16 text lost")
	}
}
func TestStandaloneSchedulesAndEscaping(t *testing.T) {
	root := "/Users/fixture/测试 & space"
	exe := root + "/bin/checkin"
	plist := RenderPlist("08:05", []string{exe, "run"}, root)
	validXML(t, plist)
	for _, part := range []string{"<integer>8</integer>", "<integer>5</integer>", "<false/>", "CHECKIN_HOME", "&amp;", "launchd.out.log", xmlText(exe)} {
		if !strings.Contains(plist, part) {
			t.Fatal("missing " + part)
		}
	}
	task := RenderWindowsTask("07:30", `C:\测试 & space\checkin.exe`, `C:\测试 & space`, `DOMAIN\fixture`)
	validXML(t, task)
	for _, part := range []string{"T07:30:00", "StartWhenAvailable", "IgnoreNew", "--home &quot;", "&amp;"} {
		if !strings.Contains(task, part) {
			t.Fatal("missing " + part)
		}
	}
}
