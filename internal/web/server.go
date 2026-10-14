package web

import (
	"checkin/internal/config"
	"checkin/internal/logx"
	"checkin/internal/paths"
	"checkin/internal/runner"
	"checkin/internal/scheduler"
	"checkin/internal/state"
	"checkin/internal/trae"
	"checkin/internal/util"
	"checkin/internal/version"
	"checkin/internal/wb"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed static/index.html
var static embed.FS

const DefaultPort = 8765
const maxBody = 64 * 1024

type Server struct {
	mu                    sync.Mutex
	running, dryRun       bool
	startedAt, finishedAt *float64
	report                any
	lines                 []string
	Schedule              *scheduler.Controller
	Stop                  context.CancelFunc
	Execute               func(context.Context, runner.Options) (runner.Report, error)
	Auth                  AuthOperations
	accountAction         AccountAction
	authCancel            context.CancelFunc
	smsRetry              map[string]time.Time
	ctx                   context.Context
}

func New(ctx context.Context) *Server {
	s := &Server{lines: []string{}, smsRetry: map[string]time.Time{}, Execute: runner.Execute, Auth: defaultAuth(), ctx: ctx}
	s.Schedule = scheduler.New(ctx, func() bool { return s.start(false, false) })
	return s
}
func (s *Server) RunSnapshot() util.M {
	s.mu.Lock()
	defer s.mu.Unlock()
	return util.M{"running": s.running, "dryRun": s.dryRun, "startedAt": s.startedAt, "finishedAt": s.finishedAt, "lines": append([]string{}, s.lines...), "report": s.report}
}
func (s *Server) start(dry, force bool) bool {
	s.mu.Lock()
	if s.ctx.Err() != nil || s.running || s.accountAction.Running {
		s.mu.Unlock()
		return false
	}
	s.running = true
	s.dryRun = dry
	s.startedAt = util.Ptr(util.Now())
	s.finishedAt = nil
	s.report = nil
	s.lines = []string{}
	s.mu.Unlock()
	go func() {
		unsubscribe := logx.Subscribe(func(line string) {
			s.mu.Lock()
			s.lines = append(s.lines, line)
			if len(s.lines) > 500 {
				s.lines = s.lines[len(s.lines)-500:]
			}
			s.mu.Unlock()
		})
		defer unsubscribe()
		report, err := s.Execute(s.ctx, runner.Options{DryRun: dry, Force: force})
		s.mu.Lock()
		if err != nil {
			s.report = util.M{"error": logx.Redact(err.Error()), "items": []any{}, "dryRun": dry}
		} else {
			s.report = report
		}
		s.running = false
		s.finishedAt = util.Ptr(util.Now())
		s.mu.Unlock()
	}()
	return true
}
func (s *Server) scheduleStatus(ctx context.Context) util.M {
	return s.Schedule.Status()
}
func (s *Server) StatusPayload(ctx context.Context) (util.M, error) {
	stored := util.ReadMap(paths.State())
	cfg, warnings, err := config.Load()
	if err != nil {
		return nil, err
	}
	history := historyView(state.History())
	ready, reason := config.DingtalkReady(cfg)
	creds := wb.LoadAll()
	accounts := []util.M{}
	usernames := map[string]string{}
	for _, cred := range creds {
		view := wb.CredentialStatus(cred)
		view["shortUid"] = wb.ShortUID(view["uid"])
		view["remain"] = util.Remain(view["expiresAt"])
		view["expiresAtText"] = util.Timestamp(view["expiresAt"])
		accounts = append(accounts, view)
		usernames[util.String(cred["uid"])] = wb.DisplayName(cred)
	}
	traeView := trae.CredentialStatus(ctx)
	traeView["remain"] = util.Remain(traeView["sessionExpires"])
	traeView["sessionExpiresText"] = util.Timestamp(traeView["sessionExpires"])
	return util.M{"version": version.Version, "date": util.Today(), "now": util.Now(), "stateDir": paths.Root(), "state": stored, "products": productRows(stored, usernames), "credentials": util.M{"trae": traeView, "workbuddy": accounts}, "auth": s.AuthSnapshot(), "points": state.Latest(history), "history": history, "config": config.Masked(cfg), "configWarnings": warnings, "secretsSet": util.M{"notify.dingtalk.webhook": cfg.Notify.Dingtalk.Webhook != "", "notify.dingtalk.secret": cfg.Notify.Dingtalk.Secret != ""}, "dingtalk": util.M{"ready": ready, "reason": reason}, "schedule": s.scheduleStatus(ctx), "run": s.RunSnapshot()}, nil
}
func hostOK(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if strings.HasPrefix(host, "[") {
		name, _, err := net.SplitHostPort(host)
		if err == nil {
			host = name
		} else if host == "[::1]" {
			host = "::1"
		} else {
			return false
		}
	} else if strings.Contains(host, ":") && host != "::1" {
		name, port, err := net.SplitHostPort(host)
		if err != nil || port == "" {
			return false
		}
		host = name
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(value)
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !hostOK(r.Host) {
		writeJSON(w, 403, util.M{"error": "Host 校验失败（本服务只服务 127.0.0.1）"})
		return
	}
	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/", "/index.html":
			data, _ := static.ReadFile("static/index.html")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(data)
		case "/favicon.ico":
			w.WriteHeader(204)
		case "/api/status":
			payload, err := s.StatusPayload(r.Context())
			if err != nil {
				writeJSON(w, 500, util.M{"error": err.Error()})
			} else {
				writeJSON(w, 200, payload)
			}
		case "/api/log":
			lines, err := strconv.Atoi(r.URL.Query().Get("lines"))
			if err != nil {
				lines = 300
			}
			if lines < 1 {
				lines = 1
			}
			if lines > 5000 {
				lines = 5000
			}
			writeJSON(w, 200, util.M{"lines": logx.Tail(lines), "path": paths.Log()})
		case "/api/auth/status":
			writeJSON(w, 200, s.AuthSnapshot())
		default:
			writeJSON(w, 404, util.M{"error": "未知路径"})
		}
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, 405, util.M{"error": "不支持的请求方法"})
		return
	}
	if r.Header.Get("X-Checkin") != "1" {
		writeJSON(w, 403, util.M{"error": "缺少 X-Checkin 请求头（跨源写入防护）"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	data, err := io.ReadAll(r.Body)
	var payload util.M
	if err != nil || len(data) > maxBody || (len(data) > 0 && (json.Unmarshal(data, &payload) != nil || payload == nil)) {
		writeJSON(w, 400, util.M{"error": "请求体必须是 JSON 对象"})
		return
	}
	if payload == nil {
		payload = util.M{}
	}
	switch r.URL.Path {
	case "/api/quit":
		if s.Stop == nil {
			writeJSON(w, 503, util.M{"error": "当前服务无法退出"})
			return
		}
		writeJSON(w, 200, util.M{"ok": true, "message": "程序正在退出，定时任务已停止"})
		s.Stop()
	case "/api/auth/login", "/api/auth/sms", "/api/auth/logout", "/api/auth/cancel":
		s.handleAuth(w, r, payload)
	case "/api/run":
		started := s.start(util.Truth(payload["dry_run"]), util.Truth(payload["force"]))
		status := 200
		if !started {
			status = 409
		}
		writeJSON(w, status, util.M{"started": started, "run": s.RunSnapshot()})
	case "/api/config":
		key := util.String(payload["key"])
		value, exists := payload["value"]
		if key == "" || !exists {
			writeJSON(w, 400, util.M{"ok": false, "message": "需要 key 与 value"})
			return
		}
		ok, msg := config.Set(key, value)
		if ok && strings.HasPrefix(key, "schedule.") {
			s.Schedule.Refresh()
		}
		status := 200
		if !ok {
			status = 400
		}
		writeJSON(w, status, util.M{"ok": ok, "message": msg})
	case "/api/schedule":
		action := util.String(payload["action"])
		var ok bool
		var msg string
		if action == "enable" || action == "install" {
			ok, msg = scheduler.Install(r.Context())
		} else if action == "disable" || action == "uninstall" {
			ok, msg = scheduler.Uninstall(r.Context())
		} else {
			writeJSON(w, 400, util.M{"ok": false, "message": "action 应为 enable / disable"})
			return
		}
		s.Schedule.Refresh()
		status := 200
		if !ok {
			status = 500
		}
		writeJSON(w, status, util.M{"ok": ok, "message": msg, "schedule": s.scheduleStatus(r.Context())})
	default:
		writeJSON(w, 404, util.M{"error": "未知路径"})
	}
}
func Bind(port int) (net.Listener, int, error) {
	tries := 10
	if port == 0 {
		tries = 1
	}
	if port < 0 || port > 65535 {
		return nil, 0, fmt.Errorf("端口应在 0-65535 之间")
	}
	var last error
	for i := 0; i < tries && port+i <= 65535; i++ {
		listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port+i))
		if err == nil {
			return listener, listener.Addr().(*net.TCPAddr).Port, nil
		}
		last = err
	}
	return nil, 0, fmt.Errorf("端口 %d 起的 %d 个端口均不可用：%w", port, tries, last)
}
func Serve(ctx context.Context, port int, openBrowser bool) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	if err := paths.Ensure(); err != nil {
		return err
	}
	if message, err := scheduler.RemoveLegacy(ctx); err != nil {
		return err
	} else if message != "" {
		fmt.Println(message)
	}
	listener, chosen, err := Bind(port)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", chosen)
	fmt.Printf("本地看板已启动：%s（仅本机可访问，Ctrl+C 或页面「退出程序」停止；定时随进程启停）\n", url)
	if openBrowser {
		launch, cancel := context.WithTimeout(ctx, 20*time.Second)
		opened := util.OpenBrowser(launch, url)
		cancel()
		if !opened {
			fmt.Printf("未能自动打开浏览器，请手动访问：%s\n", url)
		}
	}
	app := New(ctx)
	app.Stop = stop
	app.Schedule.Start()
	server := &http.Server{Handler: app, BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan struct{})
	shutdownDone := make(chan struct{})
	defer close(done)
	go func() {
		defer close(shutdownDone)
		select {
		case <-ctx.Done():
			wait, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(wait); err != nil {
				server.Close()
			}
		case <-done:
		}
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		// Serve returns as soon as Shutdown closes the listener. Wait for
		// handlers to flush /api/quit before main exits the process.
		<-shutdownDone
		fmt.Println("看板已停止")
		return nil
	}
	return err
}
