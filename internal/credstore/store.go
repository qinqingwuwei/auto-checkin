package credstore

import (
	"checkin/internal/fileperm"
	"checkin/internal/logx"
	"checkin/internal/util"
	"os"
)

func Load(path string) util.M {
	var m util.M
	if util.ReadJSON(path, &m) != nil {
		return nil
	}
	if !fileperm.Private(path, 0600) {
		logx.Warn("凭据访问权限不满足当前平台的私有文件策略：%s", path)
	}
	return m
}
func Save(path string, data util.M) error { return util.WriteJSON(path, data) }

func Remove(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
