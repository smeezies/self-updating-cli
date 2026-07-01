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
// than the running binary. It is safe to call from a background goroutine.
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

	// resolve symlinks so we get the real path on disk
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}

	destDir := filepath.Dir(exePath)

	binaryURL, checksumURL, err := findAssetURLs(release)
	if err != nil {
		return fmt.Errorf("finding assets: %w", err)
	}

	tmpFile, err := downloadAndExtractBinary(binaryURL, checksumURL, destDir)
	if err != nil {
		return fmt.Errorf("downloading binary: %w", err)
	}
	defer os.Remove(tmpFile)

	if err := os.Chmod(tmpFile, 0755); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}

	if err := CopyFile(exePath, exePath+".bak"); err != nil {
		return fmt.Errorf("creating backup: %w", err)
	}

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
// current OS and architecture within a GitHub Release's asset list.
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

// downloadAndExtractBinary downloads the tar.gz archive, verifies its SHA-256
// checksum against SHA256SUMS, then extracts the binary to a temp file.
// Returns the path to the extracted binary temp file.
func downloadAndExtractBinary(binaryURL, checksumURL string, destDir string) (string, error) {
	// download archive to a temp file (can stay in /tmp, just for download+verify)
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

	if err := verifyChecksum(archivePath, checksumURL); err != nil {
		return "", err
	}

	// extract into destDir so rename stays on the same filesystem
	return extractBinaryFromArchive(archivePath, destDir)
}

// verifyChecksum computes the SHA-256 of filePath and confirms it appears in
// the SHA256SUMS file served at checksumURL.
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

// extractBinaryFromArchive opens a .tar.gz file and extracts the first entry
// that looks like a binary (no extension, or .exe on Windows) to a temp file.
func extractBinaryFromArchive(archivePath string) (string, error) {
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

		// skip directories and metadata files
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		// match the binary: no path separator, no dot extension (or .exe)
		base := hdr.Name
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}
		isBinary := strings.HasSuffix(base, ".exe") ||
			(!strings.Contains(base, ".") && base != "")

		if !isBinary {
			continue
		}

		tmp, err := os.CreateTemp("", "update-binary-*")
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

// CopyFile copies src to dst, creating dst if it does not exist.
// Exported so cmd/app/main.go can use it for the Windows finalization step.
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
