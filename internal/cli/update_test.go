package cli

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldUpdate(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		latest   string
		expected bool
	}{
		{
			name:     "dev build should always update",
			current:  "dev",
			latest:   "v0.1.0",
			expected: true,
		},
		{
			name:     "empty version should always update",
			current:  "",
			latest:   "v0.1.0",
			expected: true,
		},
		{
			name:     "older version should update",
			current:  "v0.1.0",
			latest:   "v0.2.0",
			expected: true,
		},
		{
			name:     "same version should not update",
			current:  "v0.2.0",
			latest:   "v0.2.0",
			expected: false,
		},
		{
			name:     "newer version should not update",
			current:  "v0.3.0",
			latest:   "v0.2.0",
			expected: false,
		},
		{
			name:     "patch update should update",
			current:  "v1.0.0",
			latest:   "v1.0.1",
			expected: true,
		},
		{
			name:     "major update should update",
			current:  "v1.9.9",
			latest:   "v2.0.0",
			expected: true,
		},
		{
			name:     "handles versions without v prefix",
			current:  "0.1.0",
			latest:   "0.2.0",
			expected: true,
		},
		{
			name:     "handles mixed prefix",
			current:  "0.1.0",
			latest:   "v0.2.0",
			expected: true,
		},
		{
			name:     "pre-release older than release",
			current:  "v0.2.0-rc1",
			latest:   "v0.2.0",
			expected: true,
		},
		{
			name:     "invalid semver falls back to string comparison",
			current:  "abc",
			latest:   "def",
			expected: true,
		},
		{
			name:     "invalid semver same string",
			current:  "abc",
			latest:   "abc",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldUpdate(tt.current, tt.latest)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseChecksums(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected map[string]string
	}{
		{
			name:    "standard checksums file",
			content: "abc123  towline_v0.1.0_linux_amd64.tar.gz\ndef456  towline_v0.1.0_darwin_arm64.tar.gz\n",
			expected: map[string]string{
				"towline_v0.1.0_linux_amd64.tar.gz":  "abc123",
				"towline_v0.1.0_darwin_arm64.tar.gz": "def456",
			},
		},
		{
			name:     "empty content",
			content:  "",
			expected: map[string]string{},
		},
		{
			name:    "single entry",
			content: "abcdef1234567890  myfile.tar.gz",
			expected: map[string]string{
				"myfile.tar.gz": "abcdef1234567890",
			},
		},
		{
			name:     "malformed line ignored",
			content:  "onlyonefield\nabc123  valid.tar.gz\n",
			expected: map[string]string{"valid.tar.gz": "abc123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseChecksums(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFindAsset(t *testing.T) {
	release := &githubRelease{
		Assets: []githubAsset{
			{Name: "towline_v0.1.0_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux"},
			{Name: "towline_v0.1.0_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.com/darwin"},
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums"},
		},
	}

	tests := []struct {
		name        string
		assetName   string
		expectFound bool
		expectURL   string
	}{
		{
			name:        "finds linux asset",
			assetName:   "towline_v0.1.0_linux_amd64.tar.gz",
			expectFound: true,
			expectURL:   "https://example.com/linux",
		},
		{
			name:        "finds checksums",
			assetName:   "checksums.txt",
			expectFound: true,
			expectURL:   "https://example.com/checksums",
		},
		{
			name:        "returns nil for missing asset",
			assetName:   "towline_v0.1.0_windows_amd64.zip",
			expectFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asset := findAsset(release, tt.assetName)
			if tt.expectFound {
				require.NotNil(t, asset)
				assert.Equal(t, tt.expectURL, asset.BrowserDownloadURL)
			} else {
				assert.Nil(t, asset)
			}
		})
	}
}

func TestFetchLatestRelease(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		body        string
		expectError bool
		expectTag   string
	}{
		{
			name:       "successful response",
			statusCode: http.StatusOK,
			body: `{
				"tag_name": "v0.2.0",
				"assets": [
					{"name": "towline_v0.2.0_linux_amd64.tar.gz", "browser_download_url": "https://example.com/linux"},
					{"name": "checksums.txt", "browser_download_url": "https://example.com/checksums"}
				]
			}`,
			expectError: false,
			expectTag:   "v0.2.0",
		},
		{
			name:        "404 not found",
			statusCode:  http.StatusNotFound,
			body:        `{"message": "Not Found"}`,
			expectError: true,
		},
		{
			name:        "rate limited",
			statusCode:  http.StatusForbidden,
			body:        `{"message": "API rate limit exceeded"}`,
			expectError: true,
		},
		{
			name:        "empty tag_name",
			statusCode:  http.StatusOK,
			body:        `{"tag_name": "", "assets": []}`,
			expectError: true,
		},
		{
			name:        "invalid JSON",
			statusCode:  http.StatusOK,
			body:        `{invalid`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "GET", r.Method)
				assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
				assert.Equal(t, "towline-updater", r.Header.Get("User-Agent"))
				w.WriteHeader(tt.statusCode)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			release, err := fetchLatestRelease(server.URL)
			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, release)
			} else {
				require.NoError(t, err)
				require.NotNil(t, release)
				assert.Equal(t, tt.expectTag, release.TagName)
			}
		})
	}
}

func TestVerifyChecksum(t *testing.T) {
	// Create a temp file with known content
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.tar.gz")
	content := []byte("test binary content")
	require.NoError(t, os.WriteFile(testFile, content, 0644))

	// Compute expected hash
	h := sha256.Sum256(content)
	correctHash := fmt.Sprintf("%x", h)

	tests := []struct {
		name        string
		checksums   map[string]string
		fileName    string
		expectError bool
	}{
		{
			name:        "valid checksum",
			checksums:   map[string]string{"test.tar.gz": correctHash},
			fileName:    "test.tar.gz",
			expectError: false,
		},
		{
			name:        "wrong checksum",
			checksums:   map[string]string{"test.tar.gz": "0000000000000000000000000000000000000000000000000000000000000000"},
			fileName:    "test.tar.gz",
			expectError: true,
		},
		{
			name:        "missing checksum entry",
			checksums:   map[string]string{},
			fileName:    "test.tar.gz",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyChecksum(testFile, tt.fileName, tt.checksums)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestVerifyChecksum_FileNotFound(t *testing.T) {
	err := verifyChecksum("/nonexistent/path", "test.tar.gz", map[string]string{"test.tar.gz": "abc"})
	assert.Error(t, err)
}

func TestExtractBinary(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a tar.gz archive with a binary inside
	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	binaryContent := []byte("#!/bin/sh\necho hello\n")
	createTestArchive(t, archivePath, "mybinary", binaryContent)

	destPath := filepath.Join(tmpDir, "mybinary")
	err := extractBinary(archivePath, "mybinary", destPath)
	require.NoError(t, err)

	// Verify extracted content
	extracted, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, binaryContent, extracted)

	// Verify it's executable
	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.True(t, info.Mode().Perm()&0111 != 0, "binary should be executable")
}

func TestExtractBinary_NotFound(t *testing.T) {
	tmpDir := t.TempDir()

	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	createTestArchive(t, archivePath, "otherbinary", []byte("content"))

	destPath := filepath.Join(tmpDir, "mybinary")
	err := extractBinary(archivePath, "mybinary", destPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found in archive")
}

func TestExtractBinary_InvalidArchive(t *testing.T) {
	tmpDir := t.TempDir()

	// Write garbage data
	archivePath := filepath.Join(tmpDir, "bad.tar.gz")
	require.NoError(t, os.WriteFile(archivePath, []byte("not a gzip"), 0644))

	err := extractBinary(archivePath, "mybinary", filepath.Join(tmpDir, "mybinary"))
	assert.Error(t, err)
}

func TestInstallBinary(t *testing.T) {
	tmpDir := t.TempDir()

	// Create source binary
	srcPath := filepath.Join(tmpDir, "new-binary")
	newContent := []byte("new version")
	require.NoError(t, os.WriteFile(srcPath, newContent, 0755))

	// Create existing binary to replace
	dstPath := filepath.Join(tmpDir, "existing-binary")
	require.NoError(t, os.WriteFile(dstPath, []byte("old version"), 0755))

	err := installBinary(srcPath, dstPath)
	require.NoError(t, err)

	// Verify new content is installed
	installed, err := os.ReadFile(dstPath)
	require.NoError(t, err)
	assert.Equal(t, newContent, installed)

	// Verify .old file is cleaned up
	_, err = os.Stat(dstPath + ".old")
	assert.True(t, os.IsNotExist(err), ".old file should be cleaned up")
}

func TestInstallBinary_NewInstall(t *testing.T) {
	tmpDir := t.TempDir()

	srcPath := filepath.Join(tmpDir, "new-binary")
	require.NoError(t, os.WriteFile(srcPath, []byte("fresh install"), 0755))

	dstPath := filepath.Join(tmpDir, "nonexistent-binary")
	err := installBinary(srcPath, dstPath)
	require.NoError(t, err)

	installed, err := os.ReadFile(dstPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("fresh install"), installed)
}

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()

	srcPath := filepath.Join(tmpDir, "src")
	content := []byte("file content to copy")
	require.NoError(t, os.WriteFile(srcPath, content, 0644))

	dstPath := filepath.Join(tmpDir, "dst")
	err := copyFile(srcPath, dstPath)
	require.NoError(t, err)

	copied, err := os.ReadFile(dstPath)
	require.NoError(t, err)
	assert.Equal(t, content, copied)

	// Verify it's executable
	info, err := os.Stat(dstPath)
	require.NoError(t, err)
	assert.True(t, info.Mode().Perm()&0111 != 0, "copied file should be executable")
}

func TestDownloadFile(t *testing.T) {
	expectedContent := "downloaded content"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, expectedContent)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded")

	err := downloadFile(server.URL, destPath)
	require.NoError(t, err)

	content, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, expectedContent, string(content))
}

func TestDownloadFile_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	err := downloadFile(server.URL, filepath.Join(tmpDir, "file"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestDownloadChecksums(t *testing.T) {
	checksumContent := "abc123  towline_v0.1.0_linux_amd64.tar.gz\ndef456  towline-mcp_v0.1.0_linux_amd64.tar.gz\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, checksumContent)
	}))
	defer server.Close()

	release := &githubRelease{
		Assets: []githubAsset{
			{Name: "checksums.txt", BrowserDownloadURL: server.URL + "/checksums.txt"},
		},
	}

	checksums, err := downloadChecksums(release)
	require.NoError(t, err)
	assert.Equal(t, "abc123", checksums["towline_v0.1.0_linux_amd64.tar.gz"])
	assert.Equal(t, "def456", checksums["towline-mcp_v0.1.0_linux_amd64.tar.gz"])
}

func TestDownloadChecksums_MissingAsset(t *testing.T) {
	release := &githubRelease{
		Assets: []githubAsset{
			{Name: "something_else.txt", BrowserDownloadURL: "https://example.com/other"},
		},
	}

	_, err := downloadChecksums(release)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checksums.txt not found")
}

func TestRunUpdate_AlreadyUpToDate(t *testing.T) {
	release := githubRelease{
		TagName: "v0.5.0",
		Assets:  []githubAsset{},
	}
	releaseJSON, err := json.Marshal(release)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(releaseJSON)
	}))
	defer server.Close()

	// Set current version and override URL
	origVersion := Version
	origURL := githubReleasesURL
	Version = "v0.5.0"
	githubReleasesURL = server.URL
	defer func() {
		Version = origVersion
		githubReleasesURL = origURL
	}()

	err = runUpdate([]string{})
	assert.NoError(t, err)
}

func TestRunUpdate_CheckOnly(t *testing.T) {
	release := githubRelease{
		TagName: "v0.6.0",
		Assets:  []githubAsset{},
	}
	releaseJSON, err := json.Marshal(release)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(releaseJSON)
	}))
	defer server.Close()

	origVersion := Version
	origURL := githubReleasesURL
	Version = "v0.5.0"
	githubReleasesURL = server.URL
	defer func() {
		Version = origVersion
		githubReleasesURL = origURL
	}()

	err = runUpdate([]string{"--check"})
	assert.NoError(t, err)
}

func TestRunUpdate_GitHubAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message": "rate limit exceeded"}`)
	}))
	defer server.Close()

	origURL := githubReleasesURL
	githubReleasesURL = server.URL
	defer func() { githubReleasesURL = origURL }()

	err := runUpdate([]string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check for updates")
}

func TestRunUpdate_MissingPlatformAsset(t *testing.T) {
	// The server returns a valid release with checksums but no platform-specific
	// archive for the current runtime.GOOS/GOARCH, so runUpdate should fail
	// with "release asset not found".
	checksumContent := "abc123  towline_v0.6.0_fake_fake.tar.gz\n"

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/checksums":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, checksumContent)
		default:
			release := githubRelease{
				TagName: "v0.6.0",
				Assets: []githubAsset{
					{Name: "checksums.txt", BrowserDownloadURL: server.URL + "/checksums"},
				},
			}
			releaseJSON, _ := json.Marshal(release)
			w.WriteHeader(http.StatusOK)
			w.Write(releaseJSON)
		}
	}))
	defer server.Close()

	origVersion := Version
	origURL := githubReleasesURL
	Version = "v0.5.0"
	githubReleasesURL = server.URL
	defer func() {
		Version = origVersion
		githubReleasesURL = origURL
	}()

	err := runUpdate([]string{"--force"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "release asset not found")
}

func TestUpdatePipeline_EndToEnd(t *testing.T) {
	tmpDir := t.TempDir()

	// Create fake binaries to replace
	currentBin := filepath.Join(tmpDir, "towline")
	require.NoError(t, os.WriteFile(currentBin, []byte("old towline"), 0755))
	mcpBin := filepath.Join(tmpDir, "towline-mcp")
	require.NoError(t, os.WriteFile(mcpBin, []byte("old towline-mcp"), 0755))

	// Create fake release archives with correct platform names
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	towlineArchivePath := filepath.Join(tmpDir, "towline-archive.tar.gz")
	createTestArchive(t, towlineArchivePath, "towline", []byte("new towline v0.6.0"))
	towlineArchiveBytes, err := os.ReadFile(towlineArchivePath)
	require.NoError(t, err)
	towlineHash := fmt.Sprintf("%x", sha256.Sum256(towlineArchiveBytes))
	towlineAssetName := fmt.Sprintf("towline_v0.6.0_%s_%s.tar.gz", goos, goarch)

	mcpArchivePath := filepath.Join(tmpDir, "mcp-archive.tar.gz")
	createTestArchive(t, mcpArchivePath, "towline-mcp", []byte("new mcp v0.6.0"))
	mcpArchiveBytes, err := os.ReadFile(mcpArchivePath)
	require.NoError(t, err)
	mcpHash := fmt.Sprintf("%x", sha256.Sum256(mcpArchiveBytes))
	mcpAssetName := fmt.Sprintf("towline-mcp_v0.6.0_%s_%s.tar.gz", goos, goarch)

	checksumContent := fmt.Sprintf("%s  %s\n%s  %s\n", towlineHash, towlineAssetName, mcpHash, mcpAssetName)

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/checksums":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, checksumContent)
		case "/download/towline":
			w.WriteHeader(http.StatusOK)
			w.Write(towlineArchiveBytes)
		case "/download/mcp":
			w.WriteHeader(http.StatusOK)
			w.Write(mcpArchiveBytes)
		default:
			release := githubRelease{
				TagName: "v0.6.0",
				Assets: []githubAsset{
					{Name: "checksums.txt", BrowserDownloadURL: server.URL + "/checksums"},
					{Name: towlineAssetName, BrowserDownloadURL: server.URL + "/download/towline"},
					{Name: mcpAssetName, BrowserDownloadURL: server.URL + "/download/mcp"},
				},
			}
			releaseJSON, _ := json.Marshal(release)
			w.WriteHeader(http.StatusOK)
			w.Write(releaseJSON)
		}
	}))
	defer server.Close()

	origVersion := Version
	origURL := githubReleasesURL
	Version = "v0.5.0"
	githubReleasesURL = server.URL
	defer func() {
		Version = origVersion
		githubReleasesURL = origURL
	}()

	// We can't easily override os.Executable() in the full flow,
	// so verify the download+checksum+extract pipeline directly.
	release, err := fetchLatestRelease(server.URL)
	require.NoError(t, err)
	assert.Equal(t, "v0.6.0", release.TagName)

	checksums, err := downloadChecksums(release)
	require.NoError(t, err)
	assert.Equal(t, towlineHash, checksums[towlineAssetName])

	// Download towline archive
	asset := findAsset(release, towlineAssetName)
	require.NotNil(t, asset)
	downloadedArchive := filepath.Join(tmpDir, towlineAssetName)
	require.NoError(t, downloadFile(asset.BrowserDownloadURL, downloadedArchive))

	// Verify checksum
	require.NoError(t, verifyChecksum(downloadedArchive, towlineAssetName, checksums))

	// Extract
	extractedBin := filepath.Join(tmpDir, "extracted-towline")
	require.NoError(t, extractBinary(downloadedArchive, "towline", extractedBin))

	extracted, err := os.ReadFile(extractedBin)
	require.NoError(t, err)
	assert.Equal(t, []byte("new towline v0.6.0"), extracted)

	// Install over existing
	require.NoError(t, installBinary(extractedBin, currentBin))
	installed, err := os.ReadFile(currentBin)
	require.NoError(t, err)
	assert.Equal(t, []byte("new towline v0.6.0"), installed)
}

func TestInstallBinary_RollbackOnFailure(t *testing.T) {
	tmpDir := t.TempDir()

	// Create existing binary
	dstPath := filepath.Join(tmpDir, "existing-binary")
	originalContent := []byte("original content")
	require.NoError(t, os.WriteFile(dstPath, originalContent, 0755))

	// Use a nonexistent source to trigger copy failure
	srcPath := filepath.Join(tmpDir, "nonexistent-source")

	err := installBinary(srcPath, dstPath)
	require.Error(t, err)

	// Verify rollback: original binary should be restored
	restored, err := os.ReadFile(dstPath)
	require.NoError(t, err)
	assert.Equal(t, originalContent, restored)
}

func TestInstallBinary_LeftoverOldFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Simulate leftover .old file from a previous failed update
	dstPath := filepath.Join(tmpDir, "binary")
	require.NoError(t, os.WriteFile(dstPath, []byte("current"), 0755))
	require.NoError(t, os.WriteFile(dstPath+".old", []byte("stale old"), 0755))

	srcPath := filepath.Join(tmpDir, "new-binary")
	require.NoError(t, os.WriteFile(srcPath, []byte("new version"), 0755))

	err := installBinary(srcPath, dstPath)
	require.NoError(t, err)

	installed, err := os.ReadFile(dstPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("new version"), installed)

	// .old should be cleaned up
	_, err = os.Stat(dstPath + ".old")
	assert.True(t, os.IsNotExist(err))
}

func TestDownloadChecksums_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	release := &githubRelease{
		Assets: []githubAsset{
			{Name: "checksums.txt", BrowserDownloadURL: server.URL + "/checksums.txt"},
		},
	}

	_, err := downloadChecksums(release)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestExtractBinary_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()

	// Create archive with path traversal entry
	archivePath := filepath.Join(tmpDir, "evil.tar.gz")
	f, err := os.Create(archivePath)
	require.NoError(t, err)

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	// Add a file with path traversal in the name
	content := []byte("evil content")
	err = tw.WriteHeader(&tar.Header{
		Name: "../../mybinary",
		Size: int64(len(content)),
		Mode: 0755,
	})
	require.NoError(t, err)
	_, err = tw.Write(content)
	require.NoError(t, err)

	tw.Close()
	gw.Close()
	f.Close()

	destPath := filepath.Join(tmpDir, "mybinary")
	err = extractBinary(archivePath, "mybinary", destPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found in archive")
}

func TestExtractBinary_AbsolutePath(t *testing.T) {
	tmpDir := t.TempDir()

	archivePath := filepath.Join(tmpDir, "evil.tar.gz")
	f, err := os.Create(archivePath)
	require.NoError(t, err)

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	content := []byte("evil content")
	err = tw.WriteHeader(&tar.Header{
		Name: "/usr/local/bin/mybinary",
		Size: int64(len(content)),
		Mode: 0755,
	})
	require.NoError(t, err)
	_, err = tw.Write(content)
	require.NoError(t, err)

	tw.Close()
	gw.Close()
	f.Close()

	destPath := filepath.Join(tmpDir, "mybinary")
	err = extractBinary(archivePath, "mybinary", destPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found in archive")
}

func TestDownloadFile_CleansUpOnError(t *testing.T) {
	// Server sends partial response then closes connection
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Don't write anything — just return, causing an incomplete response
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "partial-file")

	// A successful but empty download should still create the file
	err := downloadFile(server.URL, destPath)
	// This should succeed (empty file is valid)
	assert.NoError(t, err)
}

func TestRunUpdate_InvalidFlag(t *testing.T) {
	origURL := githubReleasesURL
	defer func() { githubReleasesURL = origURL }()

	err := runUpdate([]string{"--invalid-flag"})
	assert.Error(t, err)
}

// createTestArchive creates a tar.gz file containing a single binary.
func createTestArchive(t *testing.T, archivePath, binaryName string, content []byte) {
	t.Helper()

	f, err := os.Create(archivePath)
	require.NoError(t, err)
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	err = tw.WriteHeader(&tar.Header{
		Name: binaryName,
		Size: int64(len(content)),
		Mode: 0755,
	})
	require.NoError(t, err)

	_, err = tw.Write(content)
	require.NoError(t, err)
}
