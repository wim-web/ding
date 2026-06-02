package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	defaultGitHubAPIBaseURL = "https://api.github.com"
	defaultReleaseRepo      = "wim-web/ding"
	maxReleaseAssetSize     = 100 << 20
)

func (a *app) cmdUpdate(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(a.stderr, "usage error: update takes no arguments")
		return exitUsage
	}

	latest, err := a.fetchLatestRelease()
	if err != nil {
		fmt.Fprintf(a.stderr, "update error: %v\n", err)
		return exitNetwork
	}
	if sameVersion(version, latest.TagName) {
		fmt.Fprintf(a.stdout, "ding is already up to date (%s)\n", latest.TagName)
		return exitOK
	}

	assetName := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	asset, ok := latest.findAsset(assetName)
	if !ok {
		fmt.Fprintf(a.stderr, "update error: release asset not found: %s\n", assetName)
		return exitNetwork
	}

	archive, err := a.downloadReleaseAsset(asset.BrowserDownloadURL)
	if err != nil {
		fmt.Fprintf(a.stderr, "update error: %v\n", err)
		return exitNetwork
	}
	binary, err := extractDingBinary(archive)
	if err != nil {
		fmt.Fprintf(a.stderr, "update error: %v\n", err)
		return exitConfig
	}
	if err := a.replaceExecutable(binary); err != nil {
		fmt.Fprintf(a.stderr, "update error: %v\n", err)
		return exitConfig
	}

	fmt.Fprintf(a.stdout, "updated ding to %s\n", latest.TagName)
	return exitOK
}

type githubRelease struct {
	TagName string               `json:"tag_name"`
	Assets  []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (r githubRelease) findAsset(name string) (githubReleaseAsset, bool) {
	for _, asset := range r.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return githubReleaseAsset{}, false
}

func (a *app) fetchLatestRelease() (githubRelease, error) {
	raw, err := a.getBytes(a.latestReleaseURL(), maxReleaseAssetSize)
	if err != nil {
		return githubRelease{}, err
	}
	var release githubRelease
	if err := json.Unmarshal(raw, &release); err != nil {
		return githubRelease{}, fmt.Errorf("parse latest release response: %w", err)
	}
	if release.TagName == "" {
		return githubRelease{}, errors.New("latest release response is missing tag_name")
	}
	return release, nil
}

func (a *app) latestReleaseURL() string {
	base := strings.TrimRight(a.releaseAPIBaseURL, "/")
	if base == "" {
		base = defaultGitHubAPIBaseURL
	}
	return base + "/repos/" + defaultReleaseRepo + "/releases/latest"
}

func (a *app) downloadReleaseAsset(rawURL string) ([]byte, error) {
	if rawURL == "" {
		return nil, errors.New("release asset is missing browser_download_url")
	}
	return a.getBytes(rawURL, maxReleaseAssetSize)
}

func (a *app) getBytes(rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ding/"+version)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := readAllLimit(resp.Body, limit)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("GET %s returned status %d: %s", rawURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func readAllLimit(r io.Reader, limit int64) ([]byte, error) {
	lr := &io.LimitedReader{R: r, N: limit + 1}
	body, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response is larger than %d bytes", limit)
	}
	return body, nil
}

func releaseAssetName(goos, goarch string) string {
	return fmt.Sprintf("ding_%s_%s.tar.gz", goos, goarch)
}

func extractDingBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open release archive: %w", err)
	}
	defer gz.Close()

	wanted := "ding"
	if runtime.GOOS == "windows" {
		wanted = "ding.exe"
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read release archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != wanted {
			continue
		}
		return readAllLimit(tr, maxReleaseAssetSize)
	}
	return nil, fmt.Errorf("release archive does not contain %s", wanted)
}

func (a *app) replaceExecutable(binary []byte) error {
	executable := os.Executable
	if a.executable != nil {
		executable = a.executable
	}
	exePath, err := executable()
	if err != nil {
		return err
	}
	exePath, err = filepath.Abs(exePath)
	if err != nil {
		return err
	}

	mode := os.FileMode(0o755)
	if info, err := os.Stat(exePath); err == nil {
		mode = info.Mode().Perm()
		if mode&0o111 == 0 {
			mode |= 0o111
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(exePath), ".ding-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, exePath)
}

func sameVersion(current, latest string) bool {
	current = strings.TrimPrefix(strings.TrimSpace(current), "v")
	latest = strings.TrimPrefix(strings.TrimSpace(latest), "v")
	return current != "" && latest != "" && current == latest
}
