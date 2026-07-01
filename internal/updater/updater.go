package updater

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	githubOwner = "smeezies"
	githubRepo  = "self-updating-cli"
)

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// CheckAndUpdate fetches the latest GitHub Release and applies it if newer
// than the running binary. Safe to call from a background goroutine.
func CheckAndUpdate(currentVersion string) error {
	release, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("fetching release: %w", err)
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(currentVersion, "v")

	if latest == current {
		return nil
	}

	fmt.Printf("Update available: %s -> %s\n", current, latest)

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}

	// resolve symlinks to get the real path on disk
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}

	// destination dir must be the same filesystem as the binary so
	// os.Rename works without crossing device boundaries
	destDir := filepath.Dir(exePath)

	binaryURL, checksumURL, err := findAssetURLs(release)
	if err != nil {
		return fmt.Errorf("finding assets: %w", err)
	}

	// download archive, verify checksum, extract binary into destDir
	tmpFile, err := downloadAndExtractBinary(binaryURL, checksumURL, destDir)
	if err != nil {
		return fmt.Errorf("downloading binary: %w", err)
	}
	defer os.Remove(tmpFile) // no-op if applyUpdate renamed it away

	if err := os.Chmod(tmpFile, 0755); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}

	// back up current binary before replacing
	if err := CopyFile(exePath, exePath+".bak"); err != nil {
		return fmt.Errorf("creating backup: %w", err)
	}

	// platform-specific atomic replace + re-exec
	return applyUpdate(exePath, tmpFile)
}

// fetchLatestRelease calls the GitHub Releases API and returns the latest release.
func fetchLatestRelease() (*githubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", githubOwner, githubRepo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	return &release, nil
}

// findAssetURLs locates the binary archive and SHA256SUMS file for the
// current OS and architecture in the release asset list.
func findAssetURLs(release *githubRelease) (binaryURL, checksumURL string, err error) {
	osPart := runtime.GOOS
	archPart := runtime.GOARCH

	for _, asset := range release.Assets {
		if asset.Name == "SHA256SUMS" {
			checksumURL = asset.BrowserDownloadURL
		}
		if strings.Contains(asset.Name, osPart) && strings.Contains(asset.Name, archPart) {
			binaryURL = asset.BrowserDownloadURL
		}
	}

	if binaryURL == "" {
		return "", "", fmt.Errorf("no binary asset found for %s/%s", osPart, archPart)
	}
	if checksumURL == "" {
		return "", "", fmt.Errorf("no SHA256SUMS asset found in release %s", release.TagName)
	}
	return binaryURL, checksumURL, nil
}

// downloadAndExtractBinary downloads the tar.gz archive to /tmp, verifies its
// SHA-256 checksum, then extracts the binary into destDir (same filesystem as
// the running binary so os.Rename works without crossing device boundaries).
func downloadAndExtractBinary(binaryURL, checksumURL, destDir string) (string, error) {
	// download archive to /tmp for verification (cross-device is fine here)
	resp, err := http.Get(binaryURL)
	if err != nil {
		return "", fmt.Errorf("downloading archive: %w", err)
	}
	defer resp.Body.Close()

	archiveTmp, err := os.CreateTemp("", "update-archive-*")
	if err != nil {
		return "", err
	}
	archivePath := archiveTmp.Name()
	defer os.Remove(archivePath)

	if _, err := io.Copy(archiveTmp, resp.Body); err != nil {
		archiveTmp.Close()
		return "", fmt.Errorf("writing archive: %w", err)
	}
	archiveTmp.Close()

	// verify checksum before extracting
	if err := verifyChecksum(archivePath, checksumURL); err != nil {
		return "", err
	}

	// extract binary into destDir so the subsequent os.Rename stays on one filesystem
	return extractBinaryFromArchive(archivePath, destDir)
}

// verifyChecksum computes the SHA-256 of filePath and confirms it appears
// in the SHA256SUMS file fetched from checksumURL.
func verifyChecksum(filePath, checksumURL string) error {
	resp, err := http.Get(checksumURL)
	if err != nil {
		return fmt.Errorf("fetching checksums: %w", err)
	}
	defer resp.Body.Close()

	checksumData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading checksums: %w", err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))

	if !strings.Contains(string(checksumData), actual) {
		return fmt.Errorf("checksum verification failed for %s", filePath)
	}
	return nil
}

// extractBinaryFromArchive opens a .tar.gz and extracts the first entry that
// looks like a binary into destDir. Using destDir (same fs as the exe) avoids
// cross-device rename errors when applyUpdate calls os.Rename.
func extractBinaryFromArchive(archivePath, destDir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading tar: %w", err)
		}

		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		base := hdr.Name
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}
		isBinary := strings.HasSuffix(base, ".exe") ||
			(!strings.Contains(base, ".") && base != "")

		if !isBinary {
			continue
		}

		// temp file lives in destDir so os.Rename in applyUpdate is same-device
		tmp, err := os.CreateTemp(destDir, "update-binary-*")
		if err != nil {
			return "", err
		}

		if _, err := io.Copy(tmp, tr); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return "", fmt.Errorf("extracting binary: %w", err)
		}
		tmp.Close()
		return tmp.Name(), nil
	}

	return "", fmt.Errorf("no binary found in archive %s", archivePath)
}

// CopyFile copies src to dst. Exported for use in the Windows finalization path.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
