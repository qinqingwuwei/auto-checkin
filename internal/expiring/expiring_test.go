package expiring

import (
	"checkin/internal/provider"
	"checkin/internal/util"
	"testing"
	"time"
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
	s := Summarize([]provider.Batch{batch(3, 100), batch(6, 200.5), batch(20, 300), batch(40, 400), batch(-1, 999), batch(1, -4), batch(1, 0)}, now)
	if s.Expiring3d != 100 || s.Expiring7d != 300.5 || s.ExpiringCount3d != 1 || s.ExpiringCount7d != 2 || s.BatchCount != 4 || s.TotalRemain != 1000.5 || s.Earliest3d == nil || *s.Earliest3d != now+3*Day || s.Earliest7d == nil || *s.Earliest7d != now+3*Day {
		t.Fatalf("summary %+v", s)
	}
	s = Summarize([]provider.Batch{{Remain: 10}, {Remain: 500, Expire: util.Ptr(now + Day)}, {Remain: 5, Expire: util.Ptr(now - 1)}}, now)
	if s.Expiring3d != 500 || s.Expiring7d != 500 || s.ExpiringCount3d != 1 || s.ExpiringCount7d != 1 || s.BatchCount != 2 || s.TotalRemain != 510 || s.Earliest3d == nil || *s.Earliest3d != now+Day {
		t.Fatalf("unbounded batch %+v", s)
	}
}

func TestGrantedOnCountsAllSourcesForLocalDay(t *testing.T) {
	day := time.Now()
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location()).Unix()
	batches := []provider.Batch{
		{Created: util.Ptr(float64(start)), Limit: util.Ptr(100), Name: "checkin"},
		{Created: util.Ptr(float64(start) + 59), Limit: util.Ptr(50), Name: "task"},
		{Created: util.Ptr(float64(start) - 1), Limit: util.Ptr(999), Name: "yesterday"},
		{Created: nil, Limit: util.Ptr(888), Name: "unknown"},
		{Created: util.Ptr(float64(start) + 61), Limit: nil, Name: "no-limit"},
	}
	if got := GrantedOn(batches, day); got != 150 {
		t.Fatalf("granted today %v", got)
	}
}
