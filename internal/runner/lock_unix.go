//go:build darwin || linux

package runner

import (
	"checkin/internal/paths"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func acquireRunLock() (func(), error) {
	f, err := os.OpenFile(filepath.Join(paths.Root(), ".run.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("已有签到任务正在执行，请稍后重试")
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
