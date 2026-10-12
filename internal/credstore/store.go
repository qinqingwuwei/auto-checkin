package credstore

import (
	"checkin/internal/logx"
	"checkin/internal/util"
	"os"
)

func Load(path string) util.M {
	var m util.M
	if util.ReadJSON(path, &m) != nil {
		return nil
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm() != 0600 {
		logx.Warn("凭据文件权限为 %o（建议 600）：%s", info.Mode().Perm(), path)
	}
	return m
}
func Save(path string, data util.M) error { return util.WriteJSON(path, data) }
