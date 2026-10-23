package app

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// AcquireHostLock takes an exclusive non-blocking lock on the data directory
// so a second process clearly refuses instead of racing the first one over
// the database and run state. The lock lives for the process lifetime and is
// released by the OS on exit, including after a crash.
func AcquireHostLock(dataDir string) (*os.File, error) {
	path := filepath.Join(dataDir, "host.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("IceTudio 已在此数据目录运行，同一数据目录同时只能有一个宿主进程")
	}
	return file, nil
}
