package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"checkin/internal/logx"
	"checkin/internal/util"
)

// Repo 是发布产物所在的 GitHub 仓库（公开仓库，无需凭证即可读取 releases）。
const Repo = "qinqingwuwei/auto-checkin"

const maxDownload = 64 << 20

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type Info struct {
	Current     string  `json:"current"`
	Latest      string  `json:"latest"`
	Name        string  `json:"name"`
	Notes       string  `json:"notes"`
	PublishedAt string  `json:"publishedAt"`
	HTMLURL     string  `json:"htmlUrl"`
	AssetName   string  `json:"assetName"`
	AssetURL    string  `json:"assetUrl"`
	HasUpdate   bool    `json:"hasUpdate"`
	Message     string  `json:"message"`
	CheckedAt   float64 `json:"checkedAt"`
}

type release struct {
	TagName     string  `json:"tag_name"`
	Name        string  `json:"name"`
	Body        string  `json:"body"`
	PublishedAt string  `json:"published_at"`
	HTMLURL     string  `json:"html_url"`
	Assets      []Asset `json:"assets"`
}

func platformAsset(version string) string {
	return "checkin-" + version + "-" + runtime.GOOS + "-" + runtime.GOARCH + ".zip"
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "checkin.exe"
	}
	return "checkin"
}

// Check 比对当前版本与 GitHub 最新 Release；当前为开发版或缺少本平台产物时只提示不更新。
func Check(ctx context.Context, current string) (Info, error) {
	info := Info{Current: current, CheckedAt: util.Now()}
	data, err := get(ctx, "https://api.github.com/repos/"+Repo+"/releases/latest")
	if err != nil {
		return info, err
	}
	var rel release
	if err = json.Unmarshal(data, &rel); err != nil {
		return info, fmt.Errorf("Release 响应解析失败：%w", err)
	}
	info.Latest = strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v")
	info.Name = rel.Name
	info.Notes = rel.Body
	info.PublishedAt = rel.PublishedAt
	info.HTMLURL = rel.HTMLURL
	want := platformAsset(info.Latest)
	for _, asset := range rel.Assets {
		if strings.EqualFold(asset.Name, want) {
			info.AssetName, info.AssetURL = asset.Name, asset.URL
			break
		}
	}
	_, ok := parse(current)
	switch {
	case info.Latest == "":
		info.Message = "最新 Release 缺少版本号，无法比对"
	case !ok:
		info.Message = fmt.Sprintf("当前为开发版（%s），不参与自动更新；最新正式版 %s", current, info.Latest)
	case compare(info.Latest, current) > 0:
		info.HasUpdate = true
		info.Message = fmt.Sprintf("发现新版本 %s（当前 %s）", info.Latest, current)
	default:
		info.Message = "已是最新版本（" + current + "）"
	}
	if info.HasUpdate && info.AssetURL == "" {
		info.HasUpdate = false
		info.Message = fmt.Sprintf("发现新版本 %s，但没有 %s/%s 的发布产物，请到 Release 页面手动下载", info.Latest, runtime.GOOS, runtime.GOARCH)
	}
	return info, nil
}

// Apply 下载并替换当前正在运行的可执行文件，返回替换后的路径。
// Windows 上运行中的 exe 无法被覆盖，先把旧文件改名为 .old，再放入新文件。
func Apply(ctx context.Context, info Info) (string, error) {
	if !info.HasUpdate || info.AssetURL == "" {
		return "", fmt.Errorf("没有可用的更新：%s", info.Message)
	}
	target, err := executable()
	if err != nil {
		return "", err
	}
	logx.Info("开始下载更新 %s（%s）", info.Latest, info.AssetName)
	archive, err := get(ctx, info.AssetURL)
	if err != nil {
		return "", err
	}
	if err = verifyChecksum(ctx, info, archive); err != nil {
		return "", err
	}
	binary, err := extract(archive, binaryName())
	if err != nil {
		return "", err
	}
	staged := target + ".new"
	if err = util.AtomicWrite(staged, binary, 0755); err != nil {
		return "", fmt.Errorf("写入新版本失败：%w", err)
	}
	if err = swap(target, staged); err != nil {
		os.Remove(staged)
		return "", err
	}
	logx.Info("已更新到 %s：%s", info.Latest, target)
	return target, nil
}

// Restart 用指定参数重新启动更新后的程序；调用方随后应结束当前进程。
func Restart(target string, args ...string) error {
	cmd := exec.Command(target, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("重启失败（请手动重新运行 %s）：%w", target, err)
	}
	logx.Info("新版本已启动（PID %d）", cmd.Process.Pid)
	return nil
}

// RestartArgs 返回重启时应沿用的命令行参数；双击启动（无参数）时补上默认的 ui 子命令。
func RestartArgs(openBrowser bool) []string {
	args := append([]string{}, os.Args[1:]...)
	if len(args) == 0 {
		args = []string{"ui"}
	}
	if !openBrowser && args[0] == "ui" {
		args = append(args, "--no-open")
	}
	return args
}

func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("无法定位当前程序：%w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if _, err := os.Stat(exe); err != nil {
		return "", fmt.Errorf("无法访问当前程序：%w", err)
	}
	probe := filepath.Join(filepath.Dir(exe), ".checkin-write-test")
	if err := os.WriteFile(probe, nil, 0600); err != nil {
		return "", fmt.Errorf("程序所在目录不可写（%s），请把程序放到用户可写目录后再更新", filepath.Dir(exe))
	}
	os.Remove(probe)
	return exe, nil
}

func swap(target, staged string) error {
	backup := target + ".old"
	os.Remove(backup)
	if runtime.GOOS == "windows" {
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("旧版本改名失败：%w", err)
		}
		if err := os.Rename(staged, target); err != nil {
			os.Rename(backup, target)
			return fmt.Errorf("新版本替换失败：%w", err)
		}
		return nil
	}
	return os.Rename(staged, target)
}

func verifyChecksum(ctx context.Context, info Info, archive []byte) error {
	sumName := info.AssetName + ".sha256"
	sumURL := strings.TrimSuffix(info.AssetURL, info.AssetName) + sumName
	data, err := get(ctx, sumURL)
	if err != nil {
		return fmt.Errorf("缺少校验文件 %s，已中止更新：%w", sumName, err)
	}
	want := strings.ToLower(strings.Fields(string(data))[0])
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("下载的安装包校验不一致（期望 %s），已中止更新", want)
	}
	return nil
}

func extract(archive []byte, name string) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("发布包不是合法的 zip：%w", err)
	}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || filepath.Base(file.Name) != name {
			continue
		}
		handle, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer handle.Close()
		return io.ReadAll(io.LimitReader(handle, maxDownload))
	}
	return nil, fmt.Errorf("发布包内未找到 %s", name)
}

func get(ctx context.Context, url string) ([]byte, error) {
	client := &http.Client{Timeout: 3 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "checkin-updater")
	if strings.Contains(url, "api.github.com") {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络请求失败（请检查网络连接）：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("资源不存在（HTTP 404）：仓库 %s 可能已私有化或删除，可到 %s 手动下载", Repo, "https://github.com/"+Repo+"/releases")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("下载失败（HTTP %d）", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload))
	if err != nil {
		return nil, err
	}
	return data, nil
}

// parse 解析 x.y.z 形式版本号；开发版（dev-xxxx）等非数字版本返回 false。
func parse(version string) ([]int, bool) {
	text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(version), "v"))
	text = strings.SplitN(text, "+", 2)[0]
	text = strings.SplitN(text, "-", 2)[0]
	if text == "" {
		return nil, false
	}
	parts := []int{}
	for _, part := range strings.Split(text, ".") {
		number, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		parts = append(parts, number)
	}
	return parts, true
}

func compare(latest, current string) int {
	left, ok := parse(latest)
	if !ok {
		return 0
	}
	right, _ := parse(current)
	for i := 0; i < len(left) || i < len(right); i++ {
		var a, b int
		if i < len(left) {
			a = left[i]
		}
		if i < len(right) {
			b = right[i]
		}
		if a != b {
			if a > b {
				return 1
			}
			return -1
		}
	}
	return 0
}
