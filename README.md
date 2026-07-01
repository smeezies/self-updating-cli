# self-updating-cli

A demonstration of a self-updating Go binary. When a new version is published to GitHub Releases, running instances detect the update, download and verify the new binary, and seamlessly replace themselves without any user intervention.

## How it works

The binary polls the GitHub Releases API every 4 hours in a background goroutine. When a newer version is found it downloads the release archive, verifies its SHA-256 checksum against the published `SHA256SUMS` file, extracts the binary, and replaces itself atomically.

**Linux and macOS**: the new binary is written into the same directory as the running executable, then swapped in with an atomic `os.Rename` and re-executed in place via `syscall.Exec`. The process ID does not change.

**Windows**: Windows does not allow overwriting a running executable. Instead, the new binary is placed alongside the running one as `<exe>.new`, launched with a `--finalize-update` flag, and the old process exits. The new process completes the swap, relaunches cleanly from the real path, and removes the temporary file.

In all cases a `.bak` copy of the previous binary is kept alongside the executable until the next update cycle.

## Project structure

```
self-updating-cli/
├── .github/
│   └── workflows/
│       └── release.yml          # GoReleaser trigger on v*.*.* tags
├── cmd/
│   └── app/
│       └── main.go              # Entry point, update poll loop, Windows finalize
├── internal/
│   ├── updater/
│   │   ├── updater.go           # GitHub API, download, checksum, extract
│   │   ├── updater_unix.go      # Atomic rename + syscall.Exec
│   │   └── updater_windows.go   # Spawn .new binary, exit old process
│   └── version/
│       └── version.go           # var Version = "dev" (set by ldflags at build time)
├── .goreleaser.yaml             # Cross-compile, archive, publish to GitHub Releases
└── go.mod
```

## Requirements

- Go 1.26 or later
- A GitHub repository with Releases enabled
- A GitHub personal access token with `contents: write` scope, stored as a repository secret named `GORELEASER_TOKEN` in a GitHub Actions environment named `default`

## Building locally

```bash
go build -o self-updating-cli ./cmd/app
```

To embed a version string:

```bash
go build -ldflags "-X github.com/smeezies/self-updating-cli/internal/version.Version=1.2.3" -o self-updating-cli ./cmd/app
```

To test the build pipeline without publishing a release:

```bash
goreleaser release --snapshot --clean
```

Output lands in `dist/`.

## Releasing a new version

Tag a commit with a semver tag and push it. The GitHub Actions workflow handles the rest.

```bash
git tag v1.2.3
git push origin v1.2.3
```

GoReleaser will:

1. Cross-compile binaries for Linux, macOS (Intel + Apple Silicon), and Windows
2. Package each binary as a `.tar.gz` archive (`.zip` on Windows)
3. Generate a `SHA256SUMS` file containing checksums for all archives
4. Create a GitHub Release and upload all artifacts

## Update behaviour

| Scenario | Behaviour |
|---|---|
| Already on latest version | Silent, nothing happens |
| New version available | Downloads, verifies, replaces, re-execs |
| Checksum mismatch | Aborts, temp files removed, running binary untouched |
| Download interrupted | Archive discarded, retries on next poll |
| New binary fails to launch (Windows) | Old binary remains in place |

## Configuration

To change the poll interval, update the `time.Sleep` call in `cmd/app/main.go`:

```go
time.Sleep(4 * time.Hour)  // change as needed
```

To point at a different GitHub repository, update the constants at the top of `internal/updater/updater.go`:

```go
const (
    githubOwner = "smeezies"
    githubRepo  = "self-updating-cli"
)
```

## Dependencies

None. The entire implementation uses the Go standard library only.

## Questions

 - How will we host the binary?
   - Let's use Github as its free and easy to setup a repo and push out releases
 - How will we generate the binary for different OSes?
   - Let's use Go Releaser in a Github action to easily create the binary for different OSes
 - Do we want to poll for updates or have the server push updates?
   - Polling is a lot easier and doesn't require any keeping track of clients
 - Should we keep tge previous versions around?
   - Lets keep the last version around in a .bak file just in case
 - How will we validate the download?
   - Go Releaser includes a SHA for each of the tarballs to validate the download
 - How often should we check to see if there is an update?
   - Let's check on startup and every 4 hours 
 - Should the binary have any other functionality besides updating itself?
   - No, let's keep it simple for this project
