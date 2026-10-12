package expiring

import (
	"checkin/internal/provider"
	"checkin/internal/util"
	"testing"
)

func TestWindowsAndMixedBatches(t *testing.T) {
	now := 1800000000.0
	batch := func(days, remain float64) provider.Batch {
		return provider.Batch{Remain: remain, Expire: util.Ptr(now + days*Day)}
	}
	for _, c := range []struct {
		expiry *float64
		days   int
		want   bool
	}{{util.Ptr(now), 7, false}, {util.Ptr(now + 7*Day), 7, true}, {util.Ptr(now + 7*Day + 1), 7, false}, {nil, 7, false}, {util.Ptr(now + Day), 0, false}} {
		if InWindow(c.expiry, now, c.days) != c.want {
			t.Fatalf("window %+v", c)
		}
	}
	s := Summarize([]provider.Batch{batch(3, 100), batch(6, 200.5), batch(20, 300), batch(40, 400), batch(-1, 999), batch(1, -4), batch(1, 0)}, now, 7)
	if s.ExpiringAmount != 300.5 || s.Expiring30d != 600.5 || s.ExpiringCount != 2 || s.BatchCount != 4 || s.TotalRemain != 1000.5 || s.Earliest == nil || *s.Earliest != now+3*Day {
		t.Fatalf("summary %+v", s)
	}
	s = Summarize([]provider.Batch{batch(1, 500), {Remain: 10}}, now, 0)
	if s.ExpiringAmount != 0 || s.Expiring30d != 500 || s.TotalRemain != 510 || s.Earliest != nil {
		t.Fatalf("disabled %+v", s)
	}
}
