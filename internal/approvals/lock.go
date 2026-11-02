package approvals

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// Lock prevents the running bot and onboarding CLI from writing the same Drive
// snapshot concurrently on this host. Keep the lock in the shared secrets mount.
func Lock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, errors.New("portal is already running; stop the app before using the setup command")
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
