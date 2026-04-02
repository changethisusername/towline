package cli

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// httpClient is the HTTP client used for all update requests.
// It has a 60-second timeout to prevent hanging on slow/unresponsive servers.
var httpClient = &http.Client{Timeout: 60 * time.Second}

// maxAPIResponseBytes limits JSON API response bodies to 10MB.
const maxAPIResponseBytes = 10 * 1024 * 1024

// maxDownloadBytes limits binary archive downloads to 500MB.
const maxDownloadBytes = 500 * 1024 * 1024

// githubReleasesURL is the base URL for GitHub Releases API.
// It is a variable so tests can override it.
var githubReleasesURL = "https://api.github.com/repos/changethisusername/towline/releases/latest"

// binaries lists the binaries to update.
var binaries = []string{"towline", "towline-mcp"}

// githubRelease represents the relevant fields from the GitHub Releases API.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

// githubAsset represents a single release asset.
type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "Only check for updates, don't install")
	force := fs.Bool("force", false, "Force update even if already on latest version")

	if err := fs.Parse(args); err != nil {
		return err
	}

	currentVersion := Version
	if currentVersion == "" {
		currentVersion = "dev"
	}

	fmt.Printf("Current version: %s\n", currentVersion)

	// Fetch latest release from GitHub
	fmt.Print("Checking for updates... ")
	release, err := fetchLatestRelease(githubReleasesURL)
	if err != nil {
		fmt.Println()
		return fmt.Errorf("failed to check for updates: %w", err)
	}
	fmt.Println("OK")

	latestVersion := release.TagName
	fmt.Printf("Latest version:  %s\n", latestVersion)

	// Compare versions
	if !*force && !shouldUpdate(currentVersion, latestVersion) {
		fmt.Println("\nAlready up to date.")
		return nil
	}

	if *check {
		fmt.Printf("\nUpdate available: %s → %s\n", currentVersion, latestVersion)
		fmt.Println("Run 'towline update' to install.")
		return nil
	}

	fmt.Printf("\nUpdating %s → %s\n", currentVersion, latestVersion)

	// Detect platform
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Download checksums file
	checksums, err := downloadChecksums(release)
	if err != nil {
		return fmt.Errorf("failed to download checksums: %w", err)
	}

	// Create temp directory for downloads
	tmpDir, err := os.MkdirTemp("", "towline-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Download and verify each binary.
	// Asset naming convention (from .goreleaser.yml): <binary>_<tag>_<os>_<arch>.tar.gz
	// e.g. towline_v0.2.0_darwin_arm64.tar.gz
	for _, bin := range binaries {
		archiveName := fmt.Sprintf("%s_%s_%s_%s.tar.gz", bin, latestVersion, goos, goarch)

		fmt.Printf("Downloading %s... ", archiveName)
		asset := findAsset(release, archiveName)
		if asset == nil {
			fmt.Println()
			return fmt.Errorf("release asset not found: %s (platform %s/%s may not be supported)", archiveName, goos, goarch)
		}

		archivePath := filepath.Join(tmpDir, archiveName)
		if err := downloadFile(asset.BrowserDownloadURL, archivePath); err != nil {
			fmt.Println()
			return fmt.Errorf("failed to download %s: %w", archiveName, err)
		}
		fmt.Println("OK")

		// Verify checksum
		fmt.Printf("Verifying checksum... ")
		if err := verifyChecksum(archivePath, archiveName, checksums); err != nil {
			fmt.Println()
			return fmt.Errorf("checksum verification failed for %s: %w", archiveName, err)
		}
		fmt.Println("OK")

		// Extract binary from archive
		fmt.Printf("Extracting %s... ", bin)
		extractedPath := filepath.Join(tmpDir, bin)
		if err := extractBinary(archivePath, bin, extractedPath); err != nil {
			fmt.Println()
			return fmt.Errorf("failed to extract %s: %w", bin, err)
		}
		fmt.Println("OK")
	}

	// Install binaries — find where current binary is installed
	currentExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to locate current executable: %w", err)
	}
	currentExe, err = filepath.EvalSymlinks(currentExe)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}
	installDir := filepath.Dir(currentExe)

	for _, bin := range binaries {
		extractedPath := filepath.Join(tmpDir, bin)
		targetPath := filepath.Join(installDir, bin)

		fmt.Printf("Installing %s → %s... ", bin, targetPath)
		if err := installBinary(extractedPath, targetPath); err != nil {
			fmt.Println()
			return fmt.Errorf("failed to install %s: %w", bin, err)
		}
		fmt.Println("OK")
	}

	fmt.Printf("\nTowline updated to %s\n", latestVersion)
	return nil
}

// fetchLatestRelease queries the GitHub Releases API for the latest release.
func fetchLatestRelease(url string) (*githubRelease, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "towline-updater")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, string(body))
	}

	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIResponseBytes)).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to parse release response: %w", err)
	}

	if release.TagName == "" {
		return nil, fmt.Errorf("no tag_name in release response")
	}

	return &release, nil
}

// shouldUpdate returns true if the latest version is newer than the current version.
func shouldUpdate(current, latest string) bool {
	// Dev builds should always update
	if current == "dev" || current == "" {
		return true
	}

	// Ensure versions have "v" prefix for semver comparison
	if !strings.HasPrefix(current, "v") {
		current = "v" + current
	}
	if !strings.HasPrefix(latest, "v") {
		latest = "v" + latest
	}

	// If either version is not valid semver, fall back to string comparison
	if !semver.IsValid(current) || !semver.IsValid(latest) {
		return current != latest
	}

	return semver.Compare(current, latest) < 0
}

// findAsset finds a release asset by name.
func findAsset(release *githubRelease, name string) *githubAsset {
	for i := range release.Assets {
		if release.Assets[i].Name == name {
			return &release.Assets[i]
		}
	}
	return nil
}

// downloadChecksums downloads and parses the checksums.txt file from a release.
func downloadChecksums(release *githubRelease) (map[string]string, error) {
	asset := findAsset(release, "checksums.txt")
	if asset == nil {
		return nil, fmt.Errorf("checksums.txt not found in release assets")
	}

	req, err := http.NewRequest("GET", asset.BrowserDownloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "towline-updater")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download checksums (status %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes))
	if err != nil {
		return nil, err
	}

	return parseChecksums(string(body)), nil
}

// parseChecksums parses a checksums.txt file into a map of filename -> sha256 hash.
func parseChecksums(content string) map[string]string {
	checksums := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			checksums[parts[1]] = parts[0]
		}
	}
	return checksums
}

// verifyChecksum verifies that a file matches its expected SHA256 checksum.
func verifyChecksum(filePath, fileName string, checksums map[string]string) error {
	expected, ok := checksums[fileName]
	if !ok {
		return fmt.Errorf("no checksum found for %s", fileName)
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
	if actual != expected {
		return fmt.Errorf("expected %s, got %s", expected, actual)
	}

	return nil
}

// downloadFile downloads a URL to a local file.
func downloadFile(url, dest string) (retErr error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "towline-updater")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed (status %d)", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() {
		out.Close()
		if retErr != nil {
			os.Remove(dest)
		}
	}()

	limited := io.LimitReader(resp.Body, maxDownloadBytes+1)
	n, err := io.Copy(out, limited)
	if err != nil {
		return err
	}
	if n > maxDownloadBytes {
		return fmt.Errorf("download exceeds maximum size (%d MB)", maxDownloadBytes/(1024*1024))
	}
	return out.Sync()
}

// extractBinary extracts a named binary from a tar.gz archive.
func extractBinary(archivePath, binaryName, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("failed to open gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("binary %s not found in archive", binaryName)
		}
		if err != nil {
			return fmt.Errorf("failed to read tar: %w", err)
		}

		// Reject path traversal and absolute path attempts
		if strings.Contains(hdr.Name, "..") || filepath.IsAbs(hdr.Name) {
			continue
		}

		// Match the binary name (may be at root or in a subdirectory)
		if filepath.Base(hdr.Name) == binaryName && hdr.Typeflag == tar.TypeReg {
			out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			defer out.Close()

			// Limit extraction size to 500MB to prevent decompression bombs
			_, err = io.Copy(out, io.LimitReader(tr, 500*1024*1024))
			return err
		}
	}
}

// installBinary replaces a target binary with a new one using atomic rename.
func installBinary(src, dst string) error {
	// Atomic replace: rename old binary to .old, move new one in, remove .old
	oldPath := dst + ".old"

	// Remove any leftover .old file from a previous failed update
	os.Remove(oldPath)

	// Rename current binary to .old (may not exist for towline-mcp if not installed)
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, oldPath); err != nil {
			return fmt.Errorf("failed to backup current binary: %w", err)
		}
	}

	// Copy new binary into place (cross-device safe, unlike rename from tmpdir)
	if err := copyFile(src, dst); err != nil {
		// Attempt rollback
		if _, statErr := os.Stat(oldPath); statErr == nil {
			os.Rename(oldPath, dst)
		}
		return fmt.Errorf("failed to install new binary: %w", err)
	}

	// Clean up old binary
	os.Remove(oldPath)

	return nil
}

// copyFile copies a file from src to dst, preserving executable permissions.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
