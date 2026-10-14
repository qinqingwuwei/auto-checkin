package scheduler

import (
	"checkin/internal/config"
	"checkin/internal/logx"
	"checkin/internal/util"
	"context"
	"fmt"
	"sync"
	"time"
)

// Scheduling belongs to the long-lived UI process. No OS task is registered.
type Controller struct {
	mu        sync.Mutex
	ctx       context.Context
	trigger   func() bool
	started   bool
	enabled   bool
	at        string
	next      time.Time
	lastError string
}

func New(ctx context.Context, trigger func() bool) *Controller {
	return &Controller{ctx: ctx, trigger: trigger}
}

func NextRun(now time.Time, at string) time.Time {
	var hour, minute int
	fmt.Sscanf(at, "%d:%d", &hour, &minute)
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = time.Date(now.Year(), now.Month(), now.Day()+1, hour, minute, 0, 0, now.Location())
	}
	return next
}

// A due execution is retried while the shared run queue is busy.
func (s *Controller) tick(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return
	}
	cfg, _, err := config.Load()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	s.lastError = ""
	if cfg.Schedule.Enabled != s.enabled || cfg.Schedule.Time != s.at {
		s.enabled, s.at = cfg.Schedule.Enabled, cfg.Schedule.Time
		s.next = time.Time{}
		if s.enabled {
			s.next = NextRun(now, s.at)
		}
	}
	if s.enabled && !s.next.IsZero() && !now.Before(s.next) && s.trigger() {
		logx.Info("程序内定时任务已触发（每天 %s；退出程序后停止）", s.at)
		s.next = NextRun(now, s.at)
	}
}

func (s *Controller) Start() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	s.tick(time.Now())
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case now := <-ticker.C:
				s.tick(now)
			}
		}
	}()
}

func (s *Controller) Refresh() { s.tick(time.Now()) }

func Status(context.Context) util.M {
	cfg, _, err := config.Load()
	view := util.M{"enabled": cfg.Schedule.Enabled, "running": false, "mode": "process", "backend": "程序内定时", "config_time": cfg.Schedule.Time, "nextAt": nil, "nextText": "", "message": "仅在 checkin ui 进程运行期间执行；退出程序后停止"}
	if err != nil {
		view["error"] = err.Error()
	}
	return view
}

func (s *Controller) Status() util.M {
	s.mu.Lock()
	defer s.mu.Unlock()
	view := Status(s.ctx)
	view["running"] = s.started && s.enabled && s.ctx.Err() == nil
	if view["running"] == true && !s.next.IsZero() {
		view["nextAt"] = s.next.Unix()
		view["nextText"] = s.next.Format("2006-01-02 15:04:05 MST")
	}
	if s.lastError != "" {
		view["error"] = s.lastError
	}
	return view
}

func SetEnabled(ctx context.Context, enabled bool) (bool, string) {
	if _, err := RemoveLegacy(ctx); err != nil {
		return false, err.Error()
	}
	if ok, msg := config.Set("schedule.enabled", enabled); !ok {
		return false, msg
	}
	if enabled {
		return true, "已启用程序内定时；保持 checkin ui 运行，退出程序后停止"
	}
	return true, "已关闭定时签到"
}

// Compatibility aliases no longer install a system background task.
func Install(ctx context.Context) (bool, string)   { return SetEnabled(ctx, true) }
func Uninstall(ctx context.Context) (bool, string) { return SetEnabled(ctx, false) }
