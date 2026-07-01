//go:build !windows

package updater

import (
	"os"
	"syscall"
)

// applyUpdate performs an atomic rename on POSIX filesystems then re-execs
// the new binary in place, replacing the current process image without
// changing the PID visible to the user.
func applyUpdate(exePath, tmpFile string) error {
	// os.Rename is atomic on POSIX when src and dst are on the same filesystem.
	// The temp file was created in os.TempDir(); if that's on a different
	// filesystem than the binary, Rename falls back to a copy+delete which is
	// not atomic but is still safe because the old binary is backed up.
	if err := os.Rename(tmpFile, exePath); err != nil {
		return err
	}

	// Replace the current process image with the new binary.
	// syscall.Exec does not return on success.
	return syscall.Exec(exePath, os.Args, os.Environ())
}
