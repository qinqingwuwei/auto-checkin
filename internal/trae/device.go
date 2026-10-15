package trae

import (
	"checkin/internal/credstore"
	"checkin/internal/logx"
	"checkin/internal/paths"
	"checkin/internal/util"
	"os"
	"strings"
	"sync"
)

var deviceMu sync.Mutex

// DeviceID 返回与账号绑定的稳定设备标识。
//
// Trae 的 claim 接口会校验请求头 x-device-id：IDE 客户端在每次签到/权益请求前都会
// 注入该头（main.js 的 fb()），缺失时 claim 返回 9004（"下单参数不正确"）。
//
// 客户端用的是本机自己的设备标识（ahanet 的 device_id），若我们另用一个随机 UUID，
// 同一个账号就会以「两台设备」的身份请求签到，claim 被上游风控挡下并返回 9074。
// 因此本函数优先复用本机客户端写入 ~/Library/Application Support/Trae CN/ahanet/
// tt_net_config.config 的真实标识，读不到时才回退为按账号持久化的随机 UUID。
func DeviceID(uid string) string {
	deviceMu.Lock()
	defer deviceMu.Unlock()
	store := credstore.Load(paths.TraeDevice())
	if store == nil {
		store = util.M{}
	}
	if id := ideDeviceID(paths.TraeIDEDevice()); id != "" {
		if util.String(store[uid]) != id {
			store[uid] = id
			if err := credstore.Save(paths.TraeDevice(), store); err != nil {
				logx.Warn("Trae 设备标识保存失败（本次仍复用该标识）：%s", err)
			} else {
				logx.Info("Trae 设备标识已对齐本机客户端（uid=%s）", uid)
			}
		}
		return id
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

// ideDeviceID 从 ahanet 的 tt_net_config.config 里取 device_id。
// 文件形如 `device_id&#*832753379059209@$*key&#*value@$*`；读不到时返回空串。
func ideDeviceID(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, pair := range strings.Split(string(data), "@$*") {
		key, value, ok := strings.Cut(pair, "&#*")
		if ok && strings.TrimSpace(key) == "device_id" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
