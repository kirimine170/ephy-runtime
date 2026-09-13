package recording

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

func privateRoot(path string) (*os.Root, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	for p := abs; ; p = filepath.Dir(p) {
		if st, e := os.Lstat(p); e == nil && st.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe_recording_directory")
		}
		if _, e := os.Lstat(filepath.Join(p, ".git")); e == nil {
			return nil, errors.New("recording_directory_inside_git")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	if err = os.Chmod(abs, 0700); err != nil {
		return nil, err
	}
	return os.OpenRoot(abs)
}

func readLimited(root *os.Root, name string, limit int64) ([]byte, error) {
	st, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("unsafe_recording_file")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("recording_file_capacity")
	}
	return b, err
}

// fsync both the file and parent before acknowledging an accepted event．
func atomicWrite(root *os.Root, name string, data []byte) error {
	dir := filepath.Dir(name)
	if err := root.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// os.Root confines links to its root；also reject links in the destination．
	for p := dir; p != "."; p = filepath.Dir(p) {
		st, err := root.Lstat(p)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe_recording_directory")
		}
	}
	if st, err := root.Lstat(name); err == nil && !st.Mode().IsRegular() {
		return errors.New("unsafe_recording_file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp := filepath.Join(dir, ".pending-"+uuid.NewString())
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = root.Rename(tmp, name); err != nil {
		return err
	}
	return syncDirectory(root, dir)
}
func syncDirectory(root *os.Root, dir string) error {
	f, err := root.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func removeFile(root *os.Root, name string) error {
	if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(root, filepath.Dir(name))
}
func tokenID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._:-", r) {
			return false
		}
	}
	return true
}
