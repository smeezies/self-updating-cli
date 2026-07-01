//go:build !windows

package updater

import (
	"os"
	"syscall"
)

// applyUpdate performs an atomic rename on POSIX filesystems then re-execs
// the new binary in place. Because tmpFile was created in the same directory
// as exePath (same filesystem), os.Rename is guaranteed to be atomic.
// syscall.Exec does not return on success.
func applyUpdate(exePath, tmpFile string) error {
	if err := os.Rename(tmpFile, exePath); err != nil {
		return err
	}
	return syscall.Exec(exePath, os.Args, os.Environ())
}