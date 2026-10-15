package scheduler

import (
	"checkin/internal/config"
	"checkin/internal/state"
	"checkin/internal/util"
	"context"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"
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
func TestProcessScheduleTimingBusyRetryAndStop(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	config.Set("schedule.enabled", true)
	config.Set("schedule.time", "08:05")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls, busy := 0, true
	s := New(ctx, func() bool {
		if busy {
			return false
		}
		calls++
		return true
	})
	now := time.Date(2026, 10, 13, 8, 4, 59, 0, time.FixedZone("CST", 8*3600))
	s.tick(now)
	s.tick(now.Add(time.Second))
	if calls != 0 {
		t.Fatal("busy operation was accepted")
	}
	busy = false
	s.tick(now.Add(2 * time.Second))
	s.tick(now.Add(3 * time.Second))
	if calls != 1 {
		t.Fatalf("expected one execution, got %d", calls)
	}
	config.Set("schedule.time", "08:06")
	s.tick(now.Add(4 * time.Second))
	s.tick(now.Add(61 * time.Second))
	if calls != 2 {
		t.Fatal("schedule change did not apply immediately")
	}
	config.Set("schedule.enabled", false)
	s.tick(now.Add(24 * time.Hour))
	if calls != 2 {
		t.Fatal("disabled schedule ran")
	}
	// 重新启用时仍是次日 08:04:59，尚未到 08:06 → 不补跑，也不立即执行。
	config.Set("schedule.enabled", true)
	s.tick(now.Add(24 * time.Hour))
	if calls != 2 {
		t.Fatalf("re-enable before slot should not run, got %d", calls)
	}
	cancel()
	s.tick(now.Add(48 * time.Hour))
	if calls != 2 || s.Status()["running"] != false {
		t.Fatal("schedule continued after process cancellation")
	}
}

func TestNextRunUsesLocalCalendar(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, loc)
	if next := NextRun(now, "08:00"); next.Day() != 14 || next.Hour() != 8 || next.Location() != loc {
		t.Fatalf("bad next run: %s", next)
	}
	// A DST transition changes the elapsed duration, not the requested wall time.
	dst, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now = time.Date(2026, 10, 31, 9, 0, 0, 0, dst)
	if next := NextRun(now, "08:00"); next.Hour() != 8 || next.Sub(now) != 24*time.Hour {
		t.Fatalf("DST calendar drift: %s (%s)", next, next.Sub(now))
	}
}

// 程序在设定时刻之后才启动、且当天没有任何执行记录 → 当天补跑一次。
func TestDailyCatchUpWhenSlotMissed(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	config.Set("schedule.enabled", true)
	config.Set("schedule.time", "08:00")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	s := New(ctx, func() bool { calls++; return true })
	loc := time.FixedZone("CST", 8*3600)
	s.tick(time.Date(2026, 10, 13, 10, 0, 0, 0, loc))
	if calls != 1 {
		t.Fatalf("错过时段未补跑：%d", calls)
	}
	// 同一天不重复补跑。
	s.tick(time.Date(2026, 10, 13, 10, 1, 0, 0, loc))
	if calls != 1 {
		t.Fatalf("同日重复补跑：%d", calls)
	}
	// 次日到点正常触发。
	s.tick(time.Date(2026, 10, 14, 8, 0, 0, 0, loc))
	if calls != 2 {
		t.Fatalf("次日到点未触发：%d", calls)
	}
}

// 当天已经真实执行过（无论成功或失败，含进程重启后）→ 绝不再自动重跑。
func TestNoCatchUpWhenTodayAlreadyRan(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	state.Save(util.M{"lastRunDate": "2026-10-13"})
	config.Set("schedule.enabled", true)
	config.Set("schedule.time", "08:00")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	s := New(ctx, func() bool { calls++; return true })
	s.tick(time.Date(2026, 10, 13, 10, 0, 0, 0, time.FixedZone("CST", 8*3600)))
	if calls != 0 {
		t.Fatalf("当天已执行过不应补跑：%d", calls)
	}
}

// 尚未到设定时刻不补跑，等到点再触发。
func TestNoCatchUpBeforeSlot(t *testing.T) {
	t.Setenv("CHECKIN_HOME", t.TempDir())
	config.Set("schedule.enabled", true)
	config.Set("schedule.time", "08:00")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	s := New(ctx, func() bool { calls++; return true })
	s.tick(time.Date(2026, 10, 13, 7, 0, 0, 0, time.FixedZone("CST", 8*3600)))
	if calls != 0 {
		t.Fatalf("未到设定时刻不应执行：%d", calls)
	}
}

func TestLegacyCleanupChecksDataDirectoryOwnership(t *testing.T) {
	plist := util.M{"Label": Label, "EnvironmentVariables": util.M{"CHECKIN_HOME": "/fixture/测试 & space"}}
	if !ownsPlist(plist, "/fixture/测试 & space") || ownsPlist(plist, "/different") {
		t.Fatal("macOS task ownership")
	}
	task := `<Task><Actions><Exec><Arguments>--home &quot;C:\测试 &amp; space&quot; run</Arguments></Exec></Actions></Task>`
	if owned, err := ownsWindowsTask(task, `C:\测试 & space`); err != nil || !owned {
		t.Fatalf("Windows task ownership: %v %v", owned, err)
	}
	if owned, err := ownsWindowsTask(task, `C:\different`); err != nil || owned {
		t.Fatal("another data directory would be removed")
	}
}
