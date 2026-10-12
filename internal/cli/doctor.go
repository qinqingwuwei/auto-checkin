package cli

import (
	"checkin/internal/config"
	"checkin/internal/paths"
	"checkin/internal/scheduler"
	"checkin/internal/trae"
	"checkin/internal/util"
	"checkin/internal/wb"
	"context"
	"flag"
	"fmt"
	"runtime"
	"strings"
)

type check struct{ Name, Level, Detail string }

func doctor(ctx context.Context, args []string) int {
	f := flag.NewFlagSet("doctor", flag.ContinueOnError)
	offline := f.Bool("offline", false, "跳过网络连通检查")
	if code := parse(f, args); code >= 0 {
		return code
	}
	checks := []check{}
	if err := paths.Ensure(); err != nil {
		checks = append(checks, check{"数据目录", "FAIL", err.Error()})
	} else {
		checks = append(checks, check{"数据目录", "OK", paths.Root()})
	}
	cfg, warnings, err := config.Load()
	level, detail := "OK", paths.Config()
	if err != nil {
		level = "FAIL"
		detail = err.Error()
	} else if len(warnings) > 0 {
		level = "WARN"
		detail = strings.Join(warnings, "；")
	}
	checks = append(checks, check{"配置文件", level, detail})
	ready, reason := config.DingtalkReady(cfg)
	level = "WARN"
	if ready {
		level = "OK"
	}
	checks = append(checks, check{"钉钉通知", level, reason + "（通知仍会写本地日志）"})
	sched := scheduler.Status(ctx)
	level = "WARN"
	detail = "未安装：checkin schedule install"
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		detail = "当前平台暂不支持内置定时任务，请用 cron"
	} else if util.Truth(sched["installed"]) {
		if sched["plist_time"] != sched["config_time"] {
			detail = fmt.Sprintf("已安装但时间不一致（任务 %v ≠ 配置 %v），请重新 install", sched["plist_time"], sched["config_time"])
		} else if !util.Truth(sched["loaded"]) {
			detail = "任务存在但未加载"
		} else {
			level = "OK"
			detail = "已加载，执行时间 " + util.String(sched["plist_time"])
		}
	}
	checks = append(checks, check{"定时任务", level, detail})
	channels := trae.BrowserChannels()
	level = "WARN"
	detail = "系统未安装 Chrome / Edge / Chromium；Trae 登录前请安装浏览器"
	if len(channels) > 0 {
		level = "OK"
		detail = "系统已装：" + strings.Join(channels, "、")
	}
	checks = append(checks, check{"浏览器（Trae 登录用）", level, detail})
	traeStatus := trae.CredentialStatus(ctx)
	if util.Truth(traeStatus["configured"]) {
		checks = append(checks, check{"Trae 凭据", "OK", fmt.Sprintf("会话 %s，来源 %s", util.Remain(traeStatus["sessionExpires"]), util.String(traeStatus["source"]))})
	} else {
		checks = append(checks, check{"Trae 凭据", "FAIL", util.String(traeStatus["message"])})
	}
	accounts := wb.LoadAll()
	if len(accounts) == 0 {
		checks = append(checks, check{"WorkBuddy 凭据", "FAIL", "未登录：checkin login workbuddy --method browser|sms"})
	}
	for _, cred := range accounts {
		status := wb.CredentialStatus(cred)
		level = "OK"
		if util.Truth(status["expired"]) {
			level = "FAIL"
		}
		checks = append(checks, check{"WorkBuddy 凭据（" + wb.ShortUID(status["uid"]) + "）", level, fmt.Sprintf("AT %s，%s 登录，来源 %s", util.Remain(status["expiresAt"]), util.String(status["loginMethod"]), util.String(status["source"]))})
	}
	if *offline {
		checks = append(checks, check{"网络连通", "WARN", "已跳过（--offline）"})
	} else {
		p := trae.New()
		status, e := p.Status(ctx)
		p.Close()
		level = "OK"
		detail = "今日未签到（run 时领取）"
		if e != nil {
			level = "FAIL"
			detail = e.Error()
		} else {
			if status.CheckedIn {
				detail = "今日已签到"
			}
			if status.CreditsToday != nil {
				detail += fmt.Sprintf("，今日积分 %g", *status.CreditsToday)
			}
		}
		checks = append(checks, check{"网络 · Trae", level, detail})
		if len(accounts) == 0 {
			checks = append(checks, check{"网络 · WorkBuddy", "WARN", "无账号可测（未登录）"})
		}
		for _, cred := range accounts {
			p, e := wb.New(ctx, cred)
			level = "OK"
			detail = "今日未签到（run 时领取）"
			if e == nil {
				status, err := p.Status(ctx)
				p.Close()
				e = err
				if e == nil {
					if status.CheckedIn {
						detail = "今日已签到"
					}
					if status.StreakDays != nil && *status.StreakDays != 0 {
						detail += fmt.Sprintf("，连签 %g 天", *status.StreakDays)
					}
				}
			}
			if e != nil {
				level = "FAIL"
				detail = e.Error()
			}
			checks = append(checks, check{"网络 · WorkBuddy（" + wb.ShortUID(cred["uid"]) + "）", level, detail})
		}
	}
	fmt.Println("== 自检 ==")
	failed, warned := 0, 0
	for _, c := range checks {
		fmt.Printf("  [%-4s] %s：%s\n", c.Level, c.Name, c.Detail)
		if c.Level == "FAIL" {
			failed++
		}
		if c.Level == "WARN" {
			warned++
		}
	}
	fmt.Printf("\n结果：%d 项检查，%d 项 FAIL，%d 项 WARN\n日志：%s\n", len(checks), failed, warned, paths.Log())
	if failed > 0 {
		return 1
	}
	return 0
}
