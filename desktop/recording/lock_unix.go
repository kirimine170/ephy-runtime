//go:build darwin || linux

package recording

import (
	"os"
	"syscall"
)

func lockQueue(root *os.Root) (*os.File, error) {
	f, err := root.OpenFile("writer.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
