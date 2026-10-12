package web

import (
	"checkin/internal/util"
	"checkin/internal/wb"
	"sort"
	"strings"
)

// Trae has one account card keyed by "trae|-" in the unchanged frontend.
// Normalize only the view so legacy "-" and JWT account IDs share one curve;
// keep the Python-compatible account IDs and all history unchanged on disk.
func historyView(history []util.M) []util.M {
	view := make([]util.M, 0, len(history))
	for _, row := range history {
		copy := util.M{}
		for key, value := range row {
			copy[key] = value
		}
		if copy["product"] == "trae" {
			copy["account"] = "-"
		}
		view = append(view, copy)
	}
	return view
}

func productRows(stored util.M, names map[string]string) []util.M {
	rows := []util.M{}
	for key, value := range util.Map(stored["products"]) {
		product, label, account := "trae", "Trae CN", "-"
		if key != "trae" {
			product = "workbuddy"
			parts := strings.Split(key, ":")
			account = parts[len(parts)-1]
			name := names[account]
			if name == "" {
				name = wb.ShortUID(account)
			}
			label = "WorkBuddy (" + name + ")"
		}
		row := util.M{"key": key, "label": label, "product": product, "account": account}
		for k, v := range util.Map(value) {
			row[k] = v
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i]["product"] != rows[j]["product"] {
			return rows[i]["product"] == "trae"
		}
		return util.String(rows[i]["key"]) < util.String(rows[j]["key"])
	})
	return rows
}
