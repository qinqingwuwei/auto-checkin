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
func History() []util.M   { return util.ReadJSONL(paths.Points()) }
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
