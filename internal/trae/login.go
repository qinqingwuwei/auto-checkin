package trae

import (
	"checkin/internal/credstore"
	"checkin/internal/logx"
	"checkin/internal/paths"
	"checkin/internal/util"
	"context"
	"fmt"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

var BrowserOrder = []string{"chrome", "msedge", "chromium"}

func BrowserExecutable(channel string) string {
	var candidates []string
	switch channel {
	case "chrome":
		candidates = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", filepath.Join(paths.Home(), "Applications/Google Chrome.app/Contents/MacOS/Google Chrome"), filepath.Join(os.Getenv("ProgramFiles"), `Google\Chrome\Application\chrome.exe`), filepath.Join(os.Getenv("ProgramFiles(x86)"), `Google\Chrome\Application\chrome.exe`), filepath.Join(os.Getenv("LOCALAPPDATA"), `Google\Chrome\Application\chrome.exe`)}
	case "msedge":
		candidates = []string{"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge", filepath.Join(paths.Home(), "Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"), filepath.Join(os.Getenv("ProgramFiles(x86)"), `Microsoft\Edge\Application\msedge.exe`), filepath.Join(os.Getenv("ProgramFiles"), `Microsoft\Edge\Application\msedge.exe`)}
	case "chromium":
		candidates = []string{"/Applications/Chromium.app/Contents/MacOS/Chromium", filepath.Join(paths.Home(), "Applications/Chromium.app/Contents/MacOS/Chromium"), filepath.Join(os.Getenv("LOCALAPPDATA"), "Chromium", "Application", "chrome.exe"), filepath.Join(os.Getenv("ProgramFiles"), "Chromium", "Application", "chrome.exe")}
	default:
		return ""
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	if runtime.GOOS == "linux" {
		names := map[string][]string{"chrome": {"google-chrome", "google-chrome-stable"}, "msedge": {"microsoft-edge", "microsoft-edge-stable"}, "chromium": {"chromium", "chromium-browser"}}
		for _, name := range names[channel] {
			if p, err := exec.LookPath(name); err == nil {
				return p
			}
		}
	}
	if runtime.GOOS == "windows" {
		names := map[string]string{"chrome": "chrome.exe", "msedge": "msedge.exe", "chromium": "chromium.exe"}
		if executable, err := exec.LookPath(names[channel]); err == nil {
			return executable
		}
	}
	return ""
}
func BrowserChannels() []string {
	out := []string{}
	for _, channel := range BrowserOrder {
		if BrowserExecutable(channel) != "" {
			out = append(out, channel)
		}
	}
	return out
}
func runBrowser(ctx context.Context, channel string, timeout time.Duration) (string, any, error) {
	executable := BrowserExecutable(channel)
	if executable == "" {
		return "", nil, fmt.Errorf("未找到 %s 浏览器，请安装系统 Chrome / Edge / Chromium（本工具不会下载浏览器）", channel)
	}
	if err := os.MkdirAll(paths.TraeProfile(), 0700); err != nil {
		return "", nil, err
	}
	options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	options = append(options, chromedp.ExecPath(executable), chromedp.UserDataDir(paths.TraeProfile()), chromedp.Flag("headless", false), chromedp.Flag("disable-gpu", false), chromedp.Flag("enable-automation", false), chromedp.Flag("disable-extensions", false))
	alloc, cancelAlloc := chromedp.NewExecAllocator(ctx, options...)
	defer cancelAlloc()
	browser, cancelBrowser := chromedp.NewContext(alloc)
	defer cancelBrowser()
	wait, cancelWait := context.WithTimeout(browser, timeout)
	defer cancelWait()
	if err := chromedp.Run(wait); err != nil {
		return "", nil, fmt.Errorf("启动 %s 失败：%w", channel, err)
	}
	nav, cancelNav := context.WithTimeout(wait, 60*time.Second)
	err := chromedp.Run(nav, chromedp.Navigate(LoginURL))
	cancelNav()
	if err != nil {
		logx.Warn("登录页导航告警（继续等待）：%s", err)
	}
	logx.Info("请在打开的 %s 窗口中登录 trae.cn（最多等待 %d 秒）", channel, int(timeout.Seconds()))
	for {
		var session string
		var expires any
		err = chromedp.Run(wait, chromedp.ActionFunc(func(c context.Context) error {
			cookies, e := storage.GetCookies().Do(c)
			if e != nil {
				return e
			}
			archive := util.M{}
			for _, cookie := range cookies {
				archive[cookie.Name] = util.M{"name": cookie.Name, "value": cookie.Value, "domain": cookie.Domain, "path": cookie.Path, "expires": cookie.Expires, "httpOnly": cookie.HTTPOnly, "secure": cookie.Secure, "sameSite": cookie.SameSite}
				if cookie.Name == SessionCookie && cookie.Value != "" {
					session = cookie.Value
					if cookie.Expires > 0 {
						expires = cookie.Expires
					}
				}
			}
			if session != "" {
				return credstore.Save(paths.TraeCookies(), archive)
			}
			return nil
		}))
		if err != nil {
			return "", nil, fmt.Errorf("读取登录会话失败（窗口可能已关闭或登录超时）：%w", err)
		}
		if session != "" {
			return session, expires, nil
		}
		if err = util.Sleep(wait, 2*time.Second); err != nil {
			return "", nil, fmt.Errorf("登录超时或已中断（%d 秒内未检测到 %s）", int(timeout.Seconds()), SessionCookie)
		}
	}
}
func BrowserLogin(ctx context.Context, channel string, timeout time.Duration) (util.M, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("timeout 必须大于 0")
	}
	channels := []string{channel}
	if channel == "" {
		channels = BrowserChannels()
	}
	if len(channels) == 0 {
		return nil, fmt.Errorf("系统未安装 Chrome / Edge / Chromium，请先安装浏览器")
	}
	var last error
	for _, ch := range channels {
		session, expires, err := runBrowser(ctx, ch, timeout)
		if err != nil {
			last = err
			if ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		credMu.Lock()
		if err := ctx.Err(); err != nil {
			credMu.Unlock()
			return nil, err
		}
		cred := util.M{"session": session, "sessionExpires": expires, "savedAt": time.Now().Unix(), "source": "browser:" + ch, "loginAt": time.Now().Unix()}
		err = credstore.Save(paths.TraeCred(), cred)
		credMu.Unlock()
		if err != nil {
			return nil, err
		}
		logx.Info("Trae 登录成功（通道 %s），会话有效期至 %s", ch, util.Timestamp(expires))
		return cred, nil
	}
	return nil, fmt.Errorf("浏览器登录失败：%w", last)
}
