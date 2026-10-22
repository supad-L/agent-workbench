//go:build darwin || linux

package store

import (
	"errors"
	"os"
	"syscall"
)

func acquireLock(name string) (*os.File, error) {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.Join(errors.New("数据目录已由另一个工作台实例使用"), file.Close())
	}
	return file, nil
}
