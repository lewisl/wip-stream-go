//go:build !windows

package workflow

import (
	"os"
	"syscall"
)

func processAlive(pid int) bool { return syscall.Kill(pid, 0) != syscall.ESRCH }
func ownedFile(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(s.Uid) == os.Getuid()
}
