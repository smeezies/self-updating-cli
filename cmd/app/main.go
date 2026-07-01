package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/smeezies/self-updating-cli/internal/updater"
	"github.com/smeezies/self-updating-cli/internal/version"
)

func main() {
	// Windows only: a newly downloaded binary is launched from a .new temp path
	// with this flag set to the real install path. It copies itself over the
	// real path, re-launches clean, then exits.
	finalizeTarget := flag.String("finalize-update", "", "internal: complete windows update swap")
	flag.Parse()

	if *finalizeTarget != "" {
		if err := finalizeWindowsUpdate(*finalizeTarget); err != nil {
			fmt.Fprintf(os.Stderr, "finalize-update failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("self-updating-cli %s\n", version.Version)

	// Poll for updates in the background every 4 hours.
	// On Unix a successful update re-execs the process in place.
	// On Windows it spawns the new binary and exits.
	go func() {
		for {
			if err := updater.CheckAndUpdate(version.Version); err != nil {
				fmt.Fprintf(os.Stderr, "update check failed: %v\n", err)
			}
			time.Sleep(4 * time.Hour)
		}
	}()

	// your application logic here
	fmt.Println("Running. Updates are checked in the background every 4 hours.")
	select {}
}

// finalizeWindowsUpdate is called by a newly downloaded binary running from a
// .new temp path. It copies itself over the real install path, spawns a clean
// process from there, removes the .new file, and exits.
func finalizeWindowsUpdate(targetPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding own path: %w", err)
	}

	// copy ourselves over the real install path (now writable because the
	// old process has exited and released its handle)
	if err := updater.CopyFile(exe, targetPath); err != nil {
		return fmt.Errorf("copying to target: %w", err)
	}

	// re-launch from the real path, passing through any remaining args
	cmd := exec.Command(targetPath, flag.Args()...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("relaunching: %w", err)
	}

	// remove the .new temp file and exit
	os.Remove(exe)
	os.Exit(0)
	return nil
}
