//go:build windows

package updater

import (
	"os"
	"os/exec"
	"syscall"
)

// applyUpdate handles the Windows constraint that a running executable cannot
// be overwritten. Steps:
//  1. Move the downloaded binary to <exe>.new alongside the running exe.
//  2. Launch <exe>.new with --finalize-update pointing at the real path.
//  3. Exit so Windows releases the file handle on the running exe.
//
// The new process (cmd/app/main.go) detects the flag, copies itself over the
// real path, re-launches from there, and cleans up the .new file.
func applyUpdate(exePath, tmpFile string) error {
	newPath := exePath + ".new"

	if err := os.Rename(tmpFile, newPath); err != nil {
		return err
	}

	cmd := exec.Command(newPath, append(os.Args[1:], "--finalize-update="+exePath)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

	if err := cmd.Start(); err != nil {
		os.Remove(newPath)
		return err
	}

	os.Exit(0)
	return nil
}