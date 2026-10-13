//go:build !windows

package fileperm

import "os"

func Restrict(path string, mode os.FileMode) error { return os.Chmod(path, mode) }

func Private(path string, mode os.FileMode) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().Perm() == mode
}
