package logx

import (
	"checkin/internal/paths"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

var mu sync.Mutex
var verbose bool
var sinks = map[int]func(string){}
var sequence int
var jwtPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
var secretPattern = regexp.MustCompile(`(?i)(access_token=|refresh_token=|Bearer\s+|Cloud-IDE-JWT\s+|X-Cloudide-Session[=:]\s*)([^\s&"']+)`)

func Redact(s string) string {
	s = jwtPattern.ReplaceAllString(s, "[token]")
	return secretPattern.ReplaceAllString(s, "${1}[redacted]")
}
func Setup(debug bool) error { mu.Lock(); verbose = debug; mu.Unlock(); return paths.Ensure() }
func Subscribe(fn func(string)) func() {
	mu.Lock()
	sequence++
	id := sequence
	sinks[id] = fn
	mu.Unlock()
	return func() { mu.Lock(); delete(sinks, id); mu.Unlock() }
}
func write(level, format string, args ...any) {
	msg := Redact(fmt.Sprintf(format, args...))
	now := time.Now()
	mu.Lock()
	if level != "DEBUG" || verbose {
		fmt.Fprintln(os.Stderr, msg)
	}
	if info, err := os.Stat(paths.Log()); err == nil && info.Size() >= 2*1024*1024 {
		os.Remove(paths.Log() + ".5")
		for i := 4; i >= 1; i-- {
			os.Rename(fmt.Sprintf("%s.%d", paths.Log(), i), fmt.Sprintf("%s.%d", paths.Log(), i+1))
		}
		os.Rename(paths.Log(), paths.Log()+".1")
	}
	f, err := os.OpenFile(paths.Log(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		fmt.Fprintf(f, "%s %-7s checkin: %s\n", now.Format("2006-01-02 15:04:05"), level, msg)
		f.Close()
	}
	callbacks := make([]func(string), 0, len(sinks))
	for _, fn := range sinks {
		callbacks = append(callbacks, fn)
	}
	mu.Unlock()
	line := now.Format("15:04:05") + " " + level + " " + msg
	for _, fn := range callbacks {
		fn(line)
	}
}
func Info(f string, a ...any)  { write("INFO", f, a...) }
func Warn(f string, a ...any)  { write("WARNING", f, a...) }
func Error(f string, a ...any) { write("ERROR", f, a...) }
func Debug(f string, a ...any) { write("DEBUG", f, a...) }
func Tail(lines int) []string {
	data, err := os.ReadFile(paths.Log())
	if err != nil {
		return []string{}
	}
	text := strings.TrimRight(string(data), "\r\n")
	if text == "" {
		return []string{}
	}
	rows := strings.Split(text, "\n")
	if len(rows) > lines {
		rows = rows[len(rows)-lines:]
	}
	for i := range rows {
		rows[i] = Redact(rows[i])
	}
	return rows
}
