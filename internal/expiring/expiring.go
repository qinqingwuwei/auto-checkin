package expiring

import (
	"checkin/internal/provider"
	"checkin/internal/util"
)

const Day = 86400

type Summary struct {
	Days                        int
	ExpiringAmount, Expiring30d float64
	ExpiringCount               int
	Earliest                    *float64
	BatchCount                  int
	TotalRemain                 float64
}

func InWindow(expire *float64, now float64, days int) bool {
	return expire != nil && *expire > now && days > 0 && *expire <= now+float64(days)*Day
}
func Summarize(batches []provider.Batch, now float64, days int) Summary {
	s := Summary{Days: days}
	for _, b := range batches {
		if b.Remain <= 0 || (b.Expire != nil && *b.Expire <= now) {
			continue
		}
		s.BatchCount++
		s.TotalRemain += b.Remain
		if InWindow(b.Expire, now, days) {
			s.ExpiringCount++
			s.ExpiringAmount += b.Remain
			if s.Earliest == nil || *b.Expire < *s.Earliest {
				v := *b.Expire
				s.Earliest = &v
			}
		}
		if InWindow(b.Expire, now, 30) {
			s.Expiring30d += b.Remain
		}
	}
	s.TotalRemain = util.Round(s.TotalRemain, 2)
	s.ExpiringAmount = util.Round(s.ExpiringAmount, 2)
	s.Expiring30d = util.Round(s.Expiring30d, 2)
	return s
}
