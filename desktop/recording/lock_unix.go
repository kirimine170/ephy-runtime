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

func unlockQueue(f *os.File) {
	// A concurrently forked helper can briefly inherit the open description
	// before close-on-exec runs．Unlock explicitly before releasing our fd．
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
