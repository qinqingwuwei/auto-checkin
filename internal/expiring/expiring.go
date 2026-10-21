package expiring

import (
	"checkin/internal/provider"
	"checkin/internal/util"
	"time"
)

const Day = 86400

// 只展示固定的 3 天 / 7 天两个窗口，避免「动态阈值 + 30 天」口径混用导致对不上。
const (
	ShortDays = 3
	LongDays  = 7
)

type Summary struct {
	BatchCount                       int
	TotalRemain                      float64
	Expiring3d, Expiring7d           float64
	ExpiringCount3d, ExpiringCount7d int
	Earliest3d, Earliest7d           *float64
}

func InWindow(expire *float64, now float64, days int) bool {
	return expire != nil && *expire > now && days > 0 && *expire <= now+float64(days)*Day
}

func Summarize(batches []provider.Batch, now float64) Summary {
	s := Summary{}
	for _, b := range batches {
		if b.Remain <= 0 || (b.Expire != nil && *b.Expire <= now) {
			continue
		}
		s.BatchCount++
		s.TotalRemain += b.Remain
		if InWindow(b.Expire, now, ShortDays) {
			s.ExpiringCount3d++
			s.Expiring3d += b.Remain
			s.Earliest3d = earliest(s.Earliest3d, *b.Expire)
			s.ExpiringCount7d++
			s.Expiring7d += b.Remain
			s.Earliest7d = earliest(s.Earliest7d, *b.Expire)
		} else if InWindow(b.Expire, now, LongDays) {
			s.ExpiringCount7d++
			s.Expiring7d += b.Remain
			s.Earliest7d = earliest(s.Earliest7d, *b.Expire)
		}
	}
	s.TotalRemain = util.Round(s.TotalRemain, 2)
	s.Expiring3d = util.Round(s.Expiring3d, 2)
	s.Expiring7d = util.Round(s.Expiring7d, 2)
	return s
}

func earliest(current *float64, value float64) *float64 {
	if current == nil || value < *current {
		v := value
		return &v
	}
	return current
}

// GrantedOn 统计「指定自然日当天实际入账」的积分总额：按批次创建时间（本地时区）
// 汇总 credits_limit，包含签到、月度赠送、成长任务、兑换等所有来源，而不是只看签到。
func GrantedOn(batches []provider.Batch, day time.Time) float64 {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location()).Unix()
	end := start + Day
	total := 0.0
	for _, b := range batches {
		if b.Created == nil || b.Limit == nil {
			continue
		}
		if *b.Created >= float64(start) && *b.Created < float64(end) {
			total += *b.Limit
		}
	}
	return util.Round(total, 2)
}
