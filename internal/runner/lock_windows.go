package runner

import (
	"checkin/internal/paths"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

func acquireRunLock() (func(), error) {
	f, err := os.OpenFile(filepath.Join(paths.Root(), ".run.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	lock := syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
	var overlapped syscall.Overlapped
	r, _, _ := lock.Call(f.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if r == 0 {
		f.Close()
		return nil, fmt.Errorf("已有签到或账号操作正在执行，请稍后重试")
	}
	return func() { f.Close() }, nil
}
