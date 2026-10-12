package util

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type M = map[string]any

func Map(v any) M {
	if m, ok := v.(map[string]any); ok && m != nil {
		return m
	}
	return M{}
}
func List(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}
func String(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func Or(values ...any) any {
	for _, v := range values {
		if Truth(v) {
			return v
		}
	}
	return nil
}
func Truth(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	case json.Number:
		return Float(x) != 0
	case M:
		return len(x) > 0
	case []any:
		return len(x) > 0
	}
	return true
}
func Number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		n, err := x.Float64()
		return n, err == nil
	}
	return 0, false
}
func Float(v any) float64 {
	if n, ok := Number(v); ok {
		return n
	}
	n, _ := strconv.ParseFloat(String(v), 64)
	return n
}
func NumPtr(v any) *float64 {
	if n, ok := Number(v); ok {
		return &n
	}
	return nil
}
func Ptr(v float64) *float64 { return &v }
func Round(v float64, digits int) float64 {
	// Decimal formatting rounds the original binary float, like Python round;
	// multiplying first would incorrectly turn round(2.675, 2) into 2.68.
	n, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', digits, 64), 64)
	return n
}
func Dig(obj any, keys ...string) any {
	switch x := obj.(type) {
	case M:
		names := make([]string, 0, len(x))
		for key := range x {
			names = append(names, key)
		}
		sort.Strings(names)
		for _, key := range keys {
			for _, name := range names {
				if strings.EqualFold(name, key) && x[name] != nil && x[name] != "" {
					return x[name]
				}
			}
		}
		for _, name := range names {
			if v := Dig(x[name], keys...); v != nil {
				return v
			}
		}
	case []any:
		for i, v := range x {
			if i >= 5 {
				break
			}
			if hit := Dig(v, keys...); hit != nil {
				return hit
			}
		}
	}
	return nil
}
func JWTPayload(token string) M {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return M{}
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return M{}
	}
	var m M
	if DecodeJSON(data, &m) != nil || m == nil {
		return M{}
	}
	return m
}
func JWTExp(token string) any {
	if n, ok := Number(JWTPayload(token)["exp"]); ok {
		return int64(n)
	}
	return nil
}
func MaskPhone(phone any) string {
	s := String(phone)
	if s == "" {
		return "-"
	}
	r := []rune(s)
	if len(r) < 7 {
		return s
	}
	return string(r[:3]) + "****" + string(r[len(r)-4:])
}
func MaskSecret(value any) string {
	r := []rune(String(value))
	n := len(r)
	if n == 0 {
		return "（未配置）"
	}
	if n <= 8 {
		return fmt.Sprintf("%s%s（共 %d 字符）", string(r[0]), strings.Repeat("*", n-1), n)
	}
	return fmt.Sprintf("%s…%s（共 %d 字符）", string(r[:4]), string(r[n-4:]), n)
}

var webhookToken = regexp.MustCompile(`(access_token=)([^&]+)`)

func MaskWebhook(value any) string {
	s := String(value)
	if s == "" {
		return "（未配置）"
	}
	return webhookToken.ReplaceAllStringFunc(s, func(v string) string { return "access_token=" + MaskSecret(strings.TrimPrefix(v, "access_token=")) })
}
func Today() string { return time.Now().Format("2006-01-02") }
func Now() float64  { return float64(time.Now().UnixNano()) / 1e9 }
func Timestamp(v any) string {
	n := Float(v)
	if n == 0 {
		return "-"
	}
	return time.Unix(int64(n), 0).Format("2006-01-02 15:04")
}
func Remain(v any) string {
	n := Float(v)
	if n == 0 {
		return "未知"
	}
	delta := n - Now()
	if delta <= 0 {
		return "已过期"
	}
	if delta < 3600 {
		return fmt.Sprintf("%d 分钟后过期", int(delta/60))
	}
	if delta < 48*3600 {
		return fmt.Sprintf("%.1f 小时后过期", delta/3600)
	}
	return fmt.Sprintf("%.1f 天后过期", delta/86400)
}
func ReadJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return DecodeJSON(data, dst)
}

func DecodeJSON(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("JSON 末尾存在多余数据")
	}
	return nil
}
func ReadMap(path string) M {
	var m M
	if ReadJSON(path, &m) != nil || m == nil {
		return M{}
	}
	return m
}
func WriteJSON(path string, obj any) error {
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(path, append(data, '\n'), 0600)
}
func AtomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".checkin-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}
func ReadJSONL(path string) []M {
	out := []M{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 4*1024*1024)
	for s.Scan() {
		var m M
		if DecodeJSON(s.Bytes(), &m) == nil && m != nil {
			out = append(out, m)
		}
	}
	return out
}
func AppendJSONL(path string, obj M) error {
	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	if err != nil {
		return err
	}
	return f.Sync()
}
func OpenBrowser(ctx context.Context, url string) bool {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	}
	return cmd.Run() == nil
}
func Clipboard(ctx context.Context, value string) bool {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "pbcopy")
	case "windows":
		cmd = exec.CommandContext(ctx, "clip")
	default:
		return false
	}
	cmd.Stdin = strings.NewReader(value)
	return cmd.Run() == nil
}
func Sleep(ctx context.Context, duration time.Duration) error {
	t := time.NewTimer(duration)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
