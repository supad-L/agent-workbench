//go:build !darwin && !linux

package store

import (
	"errors"
	"os"
)

func acquireLock(string) (*os.File, error) {
	return nil, errors.New("此平台的工作台实例锁尚未验证")
}
