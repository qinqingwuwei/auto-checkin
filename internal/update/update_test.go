package update

import "testing"

func TestParseRejectsDevVersions(t *testing.T) {
	if _, ok := parse("dev-20261015"); ok {
		t.Fatal("dev version should not parse")
	}
	if _, ok := parse(""); ok {
		t.Fatal("empty version should not parse")
	}
	parts, ok := parse("v1.2.3+build.5")
	if !ok || len(parts) != 3 || parts[0] != 1 || parts[2] != 3 {
		t.Fatalf("parse v1.2.3+build.5: %v %v", parts, ok)
	}
}

func TestCompareLatestAgainstCurrent(t *testing.T) {
	cases := []struct {
		latest, current string
		want            int
	}{
		{"1.2.0", "1.1.0", 1},
		{"1.1.0", "1.1.0", 0},
		{"1.0.9", "1.1.0", -1},
		{"1.0", "1.0.0", 0},
		{"dev-20261015", "1.0.0", 0}, // 开发版无法比较，视为无更新
	}
	for _, c := range cases {
		if got := compare(c.latest, c.current); got != c.want {
			t.Fatalf("compare(%q,%q)=%d want %d", c.latest, c.current, got, c.want)
		}
	}
}
