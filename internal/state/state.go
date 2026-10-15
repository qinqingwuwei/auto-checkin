package state

import (
	"checkin/internal/paths"
	"checkin/internal/util"
	"sort"
)

func Load() util.M {
	m := util.ReadMap(paths.State())
	if m["version"] == nil {
		m["version"] = 1
	}
	if m["products"] == nil {
		m["products"] = util.M{}
	}
	return m
}
func Save(m util.M) error { return util.WriteJSON(paths.State(), m) }

// LastRunDate 返回最近一次真实执行（非 dry-run）所在的自然日。
// 定时器用它判断「当天是否已经执行过」：只要执行过（无论成功或失败）就不再自动重跑，
// 避免连续触发上游限流与账号风险。
func LastRunDate() string { return util.String(Load()["lastRunDate"]) }

func History() []util.M { return util.ReadJSONL(paths.Points()) }
func Latest(history []util.M) []util.M {
	latest := map[string]util.M{}
	order := []string{}
	for _, row := range history {
		key := util.String(row["product"]) + "\x00" + util.String(row["account"])
		old, ok := latest[key]
		if !ok {
			order = append(order, key)
		}
		if !ok || util.Float(row["ts"]) > util.Float(old["ts"]) {
			latest[key] = row
		}
	}
	out := make([]util.M, 0, len(latest))
	for _, key := range order {
		out = append(out, latest[key])
	}
	sort.SliceStable(out, func(i, j int) bool { return util.String(out[i]["product"]) < util.String(out[j]["product"]) })
	return out
}
