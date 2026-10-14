package trae

import (
	"checkin/internal/credstore"
	"checkin/internal/logx"
	"checkin/internal/paths"
	"checkin/internal/util"
	"sync"
)

var deviceMu sync.Mutex

// DeviceID 返回与账号绑定的稳定设备标识。
//
// Trae 的 claim 接口会校验请求头 x-device-id：IDE 客户端在每次签到/权益请求前都会
// 注入该头（main.js 的 fb()），缺失时 claim 返回 9004（"下单参数不正确"）。
// 标识按账号（uid）分桶，首次生成随机 UUID 后持久化到
// ~/.checkin/credentials/trae-device.json，之后始终复用，不轮换，避免风控判定异常。
func DeviceID(uid string) string {
	deviceMu.Lock()
	defer deviceMu.Unlock()
	store := credstore.Load(paths.TraeDevice())
	if store == nil {
		store = util.M{}
	}
	if id := util.String(store[uid]); id != "" {
		return id
	}
	id := util.UUID4()
	store[uid] = id
	if err := credstore.Save(paths.TraeDevice(), store); err != nil {
		logx.Warn("Trae 设备标识保存失败（本次仍复用该标识）：%s", err)
	}
	return id
}
