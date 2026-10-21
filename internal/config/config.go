package config

import (
	"checkin/internal/logx"
	"checkin/internal/paths"
	"checkin/internal/util"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Config struct {
	Schedule struct {
		Time    string `json:"time"`
		Enabled bool   `json:"enabled"`
	} `json:"schedule"`
	Notify struct {
		Dingtalk struct {
			Enabled bool   `json:"enabled"`
			Webhook string `json:"webhook"`
			Secret  string `json:"secret"`
		} `json:"dingtalk"`
		Local struct {
			Enabled bool `json:"enabled"`
		} `json:"local"`
		DailySummary bool `json:"daily_summary"`
	} `json:"notify"`
	Products struct {
		Trae struct {
			Enabled bool `json:"enabled"`
		} `json:"trae"`
		WorkBuddy struct {
			Enabled bool `json:"enabled"`
		} `json:"workbuddy"`
	} `json:"products"`
}

var mu sync.Mutex
var timePattern = regexp.MustCompile(`^(\d{1,2}):(\d{1,2})$`)
var Keys = []string{"schedule.time", "schedule.enabled", "notify.dingtalk.enabled", "notify.dingtalk.webhook", "notify.dingtalk.secret", "notify.local.enabled", "notify.daily_summary", "products.trae.enabled", "products.workbuddy.enabled"}

func Defaults() Config {
	var c Config
	c.Schedule.Time = "08:00"
	c.Schedule.Enabled = true
	c.Notify.Dingtalk.Enabled = true
	c.Notify.Local.Enabled = true
	c.Products.Trae.Enabled = true
	c.Products.WorkBuddy.Enabled = true
	return c
}
func View(c Config) util.M {
	data, _ := json.Marshal(c)
	var m util.M
	json.Unmarshal(data, &m)
	return m
}
func Lookup(m util.M, key string) any {
	var node any = m
	for _, part := range strings.Split(key, ".") {
		node = util.Map(node)[part]
	}
	return node
}
func put(m util.M, key string, value any) {
	parts := strings.Split(key, ".")
	for _, part := range parts[:len(parts)-1] {
		m = util.Map(m[part])
	}
	m[parts[len(parts)-1]] = value
}
func Validate(key string, raw any) (any, error) {
	text := strings.TrimSpace(util.String(raw))
	switch key {
	case "schedule.time":
		m := timePattern.FindStringSubmatch(text)
		if m != nil {
			h, _ := strconv.Atoi(m[1])
			minute, _ := strconv.Atoi(m[2])
			if h < 24 && minute < 60 {
				return fmt.Sprintf("%02d:%02d", h, minute), nil
			}
		}
		return nil, fmt.Errorf("时间格式应为 HH:MM（24 小时制，如 08:00）")
	case "notify.dingtalk.webhook":
		if text != "" && !strings.HasPrefix(text, "http://") && !strings.HasPrefix(text, "https://") {
			return nil, fmt.Errorf("webhook 应以 http:// 或 https:// 开头")
		}
		return text, nil
	case "notify.dingtalk.secret":
		if text != "" && len([]rune(text)) < 8 {
			return nil, fmt.Errorf("加签 secret 长度不足 8 位（请检查是否复制完整）")
		}
		return text, nil
	case "schedule.enabled", "notify.dingtalk.enabled", "notify.local.enabled", "notify.daily_summary", "products.trae.enabled", "products.workbuddy.enabled":
		switch strings.ToLower(text) {
		case "true", "1", "yes", "on", "y", "t", "enabled", "开", "是":
			return true, nil
		case "false", "0", "no", "off", "n", "f", "disabled", "关", "否":
			return false, nil
		}
		return nil, fmt.Errorf("应为布尔值（true/false、yes/no、1/0、开/关）")
	default:
		keys := append([]string{}, Keys...)
		sort.Strings(keys)
		return nil, fmt.Errorf("未知配置项：%s（可用：%s）", key, strings.Join(keys, ", "))
	}
}
func load() (Config, []string, error) {
	c := Defaults()
	warnings := []string{}
	data, err := os.ReadFile(paths.Config())
	if os.IsNotExist(err) {
		err = util.WriteJSON(paths.Config(), c)
		return c, warnings, err
	}
	if err != nil {
		return c, warnings, err
	}
	var raw any
	if json.Unmarshal(data, &raw) != nil {
		warnings = append(warnings, "config.json 解析失败（不是合法 JSON），已整体回退默认值")
	} else if _, ok := raw.(map[string]any); !ok {
		warnings = append(warnings, "config.json 顶层不是对象，已整体回退默认值")
	} else {
		m := View(c)
		for _, key := range Keys {
			v := Lookup(util.Map(raw), key)
			if v == nil {
				continue
			}
			valid, e := Validate(key, v)
			if e != nil {
				warnings = append(warnings, fmt.Sprintf("%s: 值非法（%s），已回退默认值 %v", key, e, Lookup(m, key)))
			} else {
				put(m, key, valid)
			}
		}
		b, _ := json.Marshal(m)
		json.Unmarshal(b, &c)
	}
	for _, w := range warnings {
		logx.Warn("配置：%s", w)
	}
	return c, warnings, nil
}
func Load() (Config, []string, error) { mu.Lock(); defer mu.Unlock(); return load() }
func Set(key string, raw any) (bool, string) {
	v, err := Validate(key, raw)
	if err != nil {
		return false, fmt.Sprintf("%s 值非法：%s", key, err)
	}
	mu.Lock()
	defer mu.Unlock()
	c, _, err := load()
	if err != nil {
		return false, err.Error()
	}
	m := View(c)
	put(m, key, v)
	if err = util.WriteJSON(paths.Config(), m); err != nil {
		return false, err.Error()
	}
	return true, key + " = " + Display(key, v)
}
func Display(key string, value any) string {
	if strings.HasSuffix(key, "webhook") {
		return util.MaskWebhook(value)
	}
	if strings.HasSuffix(key, "secret") {
		return util.MaskSecret(value)
	}
	if b, ok := value.(bool); ok {
		if b {
			return "True"
		}
		return "False"
	}
	return util.String(value)
}
func Masked(c Config) util.M {
	m := View(c)
	put(m, "notify.dingtalk.webhook", util.MaskWebhook(c.Notify.Dingtalk.Webhook))
	put(m, "notify.dingtalk.secret", util.MaskSecret(c.Notify.Dingtalk.Secret))
	return m
}
func DingtalkReady(c Config) (bool, string) {
	if !c.Notify.Dingtalk.Enabled {
		return false, "钉钉通知已关闭（notify.dingtalk.enabled=false）"
	}
	if c.Notify.Dingtalk.Webhook == "" {
		return false, "未配置 webhook：checkin config set notify.dingtalk.webhook <URL>"
	}
	if c.Notify.Dingtalk.Secret == "" {
		return false, "未配置加签 secret：checkin config set notify.dingtalk.secret <SECRET>"
	}
	return true, "ok"
}
