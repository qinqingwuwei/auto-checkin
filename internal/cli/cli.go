package cli

import (
	"bufio"
	"checkin/internal/auth"
	"checkin/internal/config"
	"checkin/internal/logx"
	"checkin/internal/paths"
	"checkin/internal/runner"
	"checkin/internal/scheduler"
	"checkin/internal/state"
	"checkin/internal/trae"
	"checkin/internal/update"
	"checkin/internal/util"
	"checkin/internal/version"
	"checkin/internal/wb"
	"checkin/internal/web"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type onlyFlags []string

func (f *onlyFlags) String() string { return strings.Join(*f, ",") }
func (f *onlyFlags) Set(v string) error {
	if v != "trae" && v != "workbuddy" {
		return fmt.Errorf("--only 应为 trae / workbuddy")
	}
	*f = append(*f, v)
	return nil
}
func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
func parse(f *flag.FlagSet, args []string) int {
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if f.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "未知参数："+strings.Join(f.Args(), " "))
		return 2
	}
	return -1
}
func help() {
	fmt.Println(`checkin · Trae CN + WorkBuddy 签到与积分助手

用法：checkin [-v] <命令> [参数]
  run       执行签到、积分台账与到期预警（--dry-run / --force / --only）
  status    查看本地状态（--json）
  doctor    检查配置、凭据、网络与定时（--offline）
  login     trae / workbuddy（--method browser|sms）
  logout    trae / workbuddy --uid <账号 UID>（清除本地凭据）
  config    show / get <点路径> / set <点路径> <值> / path
  schedule  enable / disable / status（定时随 ui 进程启停）
  update    检查并安装新版本（--check 只检查）
  ui        本地 Web 看板（--port 8765 / --no-open）
  --version 查看版本

不带命令启动本地看板；关闭进程或在页面点击「退出程序」停止定时。`)
}
func Main(ctx context.Context, args []string) int {
	root := flag.NewFlagSet("checkin", flag.ContinueOnError)
	debug := root.Bool("verbose", false, "输出调试日志")
	root.BoolVar(debug, "v", false, "输出调试日志")
	showVersion := root.Bool("version", false, "查看版本")
	home := root.String("home", "", "覆盖数据目录（等同 CHECKIN_HOME）")
	root.Usage = help
	if err := root.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Println("checkin " + version.Version)
		return 0
	}
	if *home != "" {
		os.Setenv("CHECKIN_HOME", *home)
	}
	if err := logx.Setup(*debug); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	args = root.Args()
	if len(args) == 0 {
		args = []string{"ui"}
	}
	switch args[0] {
	case "run":
		return run(ctx, args[1:])
	case "status":
		return status(ctx, args[1:])
	case "doctor":
		return doctor(ctx, args[1:])
	case "login":
		return login(ctx, args[1:])
	case "logout":
		return logout(args[1:])
	case "config":
		return configCommand(args[1:])
	case "schedule":
		return schedule(ctx, args[1:])
	case "update":
		return updateCommand(ctx, args[1:])
	case "ui":
		f := flag.NewFlagSet("ui", flag.ContinueOnError)
		port := f.Int("port", web.DefaultPort, "监听端口（默认 8765，被占用时自动顺延）")
		noOpen := f.Bool("no-open", false, "不自动打开浏览器")
		if code := parse(f, args[1:]); code >= 0 {
			return code
		}
		if err := web.Serve(ctx, *port, !*noOpen); err != nil {
			logx.Error("本地看板启动失败：%s", err)
			return 1
		}
		return 0
	case "help":
		help()
		return 0
	default:
		fmt.Fprintln(os.Stderr, "未知命令："+args[0])
		help()
		return 2
	}
}
func run(ctx context.Context, args []string) int {
	f := flag.NewFlagSet("run", flag.ContinueOnError)
	dry := f.Bool("dry-run", false, "不领取、不落 state/points、不发通知")
	force := f.Bool("force", false, "忽略今日幂等")
	var only onlyFlags
	f.Var(&only, "only", "只跑 trae / workbuddy（可重复）")
	if code := parse(f, args); code >= 0 {
		return code
	}
	report, err := runner.Execute(ctx, runner.Options{DryRun: *dry, Force: *force, Only: only})
	if err != nil {
		fmt.Fprintln(os.Stderr, "执行失败："+err.Error())
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	fmt.Println()
	suffix := ""
	if report.DryRun {
		suffix = "，dry-run"
	}
	fmt.Printf("== 执行结果（%s%s）==\n", report.Date, suffix)
	failures := 0
	for _, item := range report.Items {
		fmt.Println("  " + runner.ItemLine(item))
		if item.Error {
			failures++
		}
	}
	if failures > 0 {
		fmt.Printf("\n有 %d 项需要处理（详见日志：%s）\n", failures, paths.Log())
		return 1
	}
	return 0
}
func StatusPayload(ctx context.Context) util.M {
	accounts := []util.M{}
	for _, cred := range wb.LoadAll() {
		accounts = append(accounts, wb.CredentialStatus(cred))
	}
	return util.M{"date": util.Today(), "state": util.ReadMap(paths.State()), "credentials": util.M{"trae": trae.CredentialStatus(ctx), "workbuddy": accounts}, "points": state.Latest(state.History())}
}
func status(ctx context.Context, args []string) int {
	f := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOutput := f.Bool("json", false, "输出 JSON")
	if code := parse(f, args); code >= 0 {
		return code
	}
	payload := StatusPayload(ctx)
	if *jsonOutput {
		printJSON(payload)
		return 0
	}
	stored := util.Map(payload["state"])
	products := util.Map(stored["products"])
	credentials := util.Map(payload["credentials"])
	traeStatus := util.Map(credentials["trae"])
	fmt.Printf("== 签到状态（%s）==\n", util.Today())
	if len(products) == 0 {
		fmt.Println("  暂无执行记录（先跑一次：checkin run --dry-run）")
	}
	keys := []string{}
	for key := range products {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		record := util.Map(products[key])
		marker := "WARN"
		if record["successDate"] == util.Today() {
			marker = "OK"
		}
		fmt.Printf("  [%s] %s：%s（最近 %s，成功日 %s）\n", marker, key, util.String(util.Or(record["message"], "-")), util.String(util.Or(record["at"], "-")), util.String(util.Or(record["successDate"], "-")))
	}
	fmt.Println("\n== 凭据 ==")
	if util.Truth(traeStatus["configured"]) {
		fmt.Printf("  [OK] Trae CN：会话 %s，来源 %s\n", util.Remain(traeStatus["sessionExpires"]), util.String(traeStatus["source"]))
	} else {
		fmt.Printf("  [WARN] Trae CN：%s\n", util.String(traeStatus["message"]))
	}
	accounts := payload["credentials"].(map[string]any)["workbuddy"].([]util.M)
	if len(accounts) == 0 {
		fmt.Println("  [WARN] WorkBuddy：未登录（checkin login workbuddy）")
	}
	for _, acc := range accounts {
		flag := "OK"
		if util.Truth(acc["expired"]) {
			flag = "FAIL"
		}
		fmt.Printf("  [%s] WorkBuddy（%s）：AT %s，登录方式 %s，手机号 %s\n", flag, wb.ShortUID(acc["uid"]), util.Remain(acc["expiresAt"]), util.String(acc["loginMethod"]), util.String(acc["phone"]))
	}
	fmt.Println("\n== 积分（最近快照）==")
	points := payload["points"].([]util.M)
	if len(points) == 0 {
		fmt.Println("  暂无积分快照（先跑一次：checkin run）")
	}
	for _, row := range points {
		account := []rune(util.String(row["account"]))
		if len(account) > 8 {
			account = account[:8]
		}
		fmt.Printf("  %s %s/%s：余额 %v，%v 天内到期 %v，30 天内到期 %v\n", util.String(row["date"]), util.String(row["product"]), string(account), row["balance"], row["expiring_days"], row["expiring_amount"], row["expiring_30d"])
	}
	fmt.Println("\n日志：" + paths.Log())
	return 0
}
func configCommand(args []string) int {
	if len(args) == 0 {
		fmt.Println("用法：checkin config show|get|set|path")
		return 2
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Println("用法：checkin config show|get <点路径>|set <点路径> <值>|path")
		return 0
	}
	switch args[0] {
	case "path":
		if len(args) != 1 {
			return 2
		}
		fmt.Println(paths.Config())
		return 0
	case "show":
		if len(args) != 1 {
			return 2
		}
		cfg, warnings, err := config.Load()
		if err != nil {
			fmt.Println(err)
			return 1
		}
		printJSON(config.Masked(cfg))
		if len(warnings) > 0 {
			fmt.Println("\n警告：" + strings.Join(warnings, "；"))
		}
		return 0
	case "get":
		if len(args) != 2 {
			fmt.Println("用法：checkin config get <点路径>")
			return 2
		}
		cfg, _, err := config.Load()
		if err != nil {
			fmt.Println(err)
			return 1
		}
		value := config.Lookup(config.View(cfg), args[1])
		if value == nil {
			fmt.Println("未知配置项：" + args[1])
			return 2
		}
		if m, ok := value.(map[string]any); ok {
			// A subtree can contain credentials; use the same masked view as config show.
			m = util.Map(config.Lookup(config.Masked(cfg), args[1]))
			printJSON(m)
		} else {
			fmt.Println(config.Display(args[1], value))
		}
		return 0
	case "set":
		if len(args) != 3 {
			fmt.Println("用法：checkin config set <点路径> <值>")
			return 2
		}
		ok, msg := config.Set(args[1], args[2])
		fmt.Println(msg)
		if !ok {
			return 2
		}
		return 0
	default:
		fmt.Println("未知配置操作：" + args[0])
		return 2
	}
}
func schedule(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Println("用法：checkin schedule enable|disable|status（仅 ui 进程运行时执行）")
		return 2
	}
	var ok bool
	var msg string
	switch args[0] {
	case "status":
		printJSON(scheduler.Status(ctx))
		return 0
	case "enable", "install":
		ok, msg = scheduler.Install(ctx)
	case "disable", "uninstall":
		ok, msg = scheduler.Uninstall(ctx)
	case "--help", "-h":
		fmt.Println("用法：checkin schedule enable|disable|status（install/uninstall 是兼容别名，不注册系统任务）")
		return 0
	default:
		return 2
	}
	fmt.Println(msg)
	if !ok {
		return 1
	}
	return 0
}
func updateCommand(ctx context.Context, args []string) int {
	f := flag.NewFlagSet("update", flag.ContinueOnError)
	checkOnly := f.Bool("check", false, "只检查更新，不下载安装")
	if code := parse(f, args); code >= 0 {
		return code
	}
	info, err := update.Check(ctx, version.Version)
	if err != nil {
		fmt.Println("检查更新失败：" + err.Error())
		return 1
	}
	fmt.Printf("当前版本 %s，最新版本 %s\n", info.Current, info.Latest)
	if !info.HasUpdate {
		fmt.Println(info.Message)
		return 0
	}
	if info.Notes != "" {
		fmt.Println("更新说明：\n" + info.Notes)
	}
	if *checkOnly {
		fmt.Println("发现新版本，运行 checkin update 可下载安装。")
		return 0
	}
	target, err := update.Apply(ctx, info)
	if err != nil {
		fmt.Println("更新失败：" + err.Error())
		return 1
	}
	fmt.Printf("已更新到 %s（%s）\n", info.Latest, target)
	fmt.Println("请重启程序以生效：重新运行 checkin，或在 WebUI 点「退出程序」后重新启动。")
	return 0
}
func login(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Println("用法：checkin login trae|workbuddy [参数]")
		return 2
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Println("用法：checkin login trae [--channel chrome|msedge|chromium] [--timeout 480]\n      checkin login workbuddy [--method browser|sms] [--phone 手机号] [--code 验证码] [--timeout 300]")
		return 0
	}
	if args[0] == "trae" {
		f := flag.NewFlagSet("login trae", flag.ContinueOnError)
		channel := f.String("channel", "", "chrome / msedge / chromium")
		timeout := f.Float64("timeout", trae.LoginWait.Seconds(), "等待登录秒数")
		if code := parse(f, args[1:]); code >= 0 {
			return code
		}
		if *channel != "" && *channel != "chrome" && *channel != "msedge" && *channel != "chromium" {
			fmt.Println("未知浏览器通道：" + *channel)
			return 2
		}
		msg, err := auth.Login(ctx, auth.LoginOptions{Product: "trae", Method: "browser", Channel: *channel, Timeout: time.Duration(*timeout * float64(time.Second))}, nil)
		if err != nil {
			printLoginError(err)
			return 1
		}
		fmt.Println(msg)
		return 0
	}
	if args[0] != "workbuddy" {
		fmt.Println("未知产品：" + args[0])
		return 2
	}
	f := flag.NewFlagSet("login workbuddy", flag.ContinueOnError)
	method := f.String("method", "browser", "browser / sms")
	phone := f.String("phone", "", "手机号")
	code := f.String("code", "", "已有验证码（提供时直接登录，不重复发码）")
	timeout := f.Float64("timeout", wb.OAuthWait.Seconds(), "浏览器登录等待秒数")
	if status := parse(f, args[1:]); status >= 0 {
		return status
	}
	if *method == "browser" {
		fmt.Printf("将用系统默认浏览器打开登录页，请在 %d 秒内完成登录（微信扫码）…\n", int(*timeout))
	} else if *method == "sms" {
		input := bufio.NewReader(os.Stdin)
		interactive := func() bool { info, e := os.Stdin.Stat(); return e == nil && info.Mode()&os.ModeCharDevice != 0 }
		if *phone == "" {
			if !interactive() {
				fmt.Println("非交互环境请提供 --phone <手机号>")
				return 2
			}
			fmt.Print("手机号：")
			*phone, _ = input.ReadString('\n')
			*phone = strings.TrimSpace(*phone)
		}
		if *phone == "" {
			fmt.Println("未提供手机号")
			return 2
		}
		if *code == "" {
			ok, msg := wb.SendSMS(ctx, *phone)
			fmt.Println(msg)
			if !ok {
				return 1
			}
			if !interactive() {
				fmt.Println("非交互环境请追加 --code <验证码>（验证码有效期内直接登录，不重复发送）")
				return 2
			}
			fmt.Print("验证码：")
			*code, _ = input.ReadString('\n')
			*code = strings.TrimSpace(*code)
		}
		if *code == "" {
			fmt.Println("未输入验证码")
			return 2
		}
	} else {
		fmt.Println("--method 应为 browser / sms")
		return 2
	}
	msg, err := auth.Login(ctx, auth.LoginOptions{Product: "workbuddy", Method: *method, Phone: *phone, Code: *code, Timeout: time.Duration(*timeout * float64(time.Second))}, nil)
	if err != nil {
		printLoginError(err)
		return 1
	}
	fmt.Println(msg)
	return 0
}

// printLoginError 统一登录失败输出：上游文案可能自带「登录失败：」前缀，避免重复打印。
func printLoginError(err error) {
	fmt.Println("登录失败：" + strings.TrimPrefix(err.Error(), "登录失败："))
}

func logout(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("用法：checkin logout trae\n      checkin logout workbuddy --uid <账号 UID>（通过 status --json 查看）")
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	f := flag.NewFlagSet("logout", flag.ContinueOnError)
	uid := f.String("uid", "", "要登出的 WorkBuddy 账号 UID")
	if code := parse(f, args[1:]); code >= 0 {
		return code
	}
	if err := auth.ValidateLogout(args[0], *uid); err != nil {
		fmt.Println(err)
		return 2
	}
	if err := auth.Logout(args[0], *uid); err != nil {
		fmt.Println("登出失败：" + err.Error())
		return 1
	}
	fmt.Println("已清除本工具的登录凭据；积分历史保留。其他客户端的登录状态不受影响。")
	return 0
}
