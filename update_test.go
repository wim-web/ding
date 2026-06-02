package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpdateReplacesExecutableFromLatestRelease(t *testing.T) {
	oldVersion := version
	version = "dev"
	defer func() {
		version = oldVersion
	}()

	assetName := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	newBinary := []byte("new ding binary")
	archive := makeReleaseArchive(t, newBinary)

	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/wim-web/ding/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[{"name":%q,"browser_download_url":%q}]}`, assetName, serverURL+"/download")
		case "/download":
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	serverURL = server.URL
	defer server.Close()

	exePath := filepath.Join(t.TempDir(), "ding")
	if runtime.GOOS == "windows" {
		exePath += ".exe"
	}
	if err := os.WriteFile(exePath, []byte("old ding binary"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	a, stdout, stderr := newTestApp(t, t.TempDir(), "", false, server.Client())
	a.releaseAPIBaseURL = server.URL
	a.executable = func() (string, error) { return exePath, nil }

	if code := a.run([]string{"update"}); code != exitOK {
		t.Fatalf("update exit = %d, stderr = %s", code, stderr.String())
	}
	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read executable: %v", err)
	}
	if string(got) != string(newBinary) {
		t.Fatalf("updated executable = %q, want %q", got, newBinary)
	}
	if !strings.Contains(stdout.String(), "updated ding to v9.9.9") {
		t.Fatalf("stdout = %q, want update message", stdout.String())
	}
}

func TestUpdateAlreadyCurrentSkipsReplacement(t *testing.T) {
	oldVersion := version
	version = "v9.9.9"
	defer func() {
		version = oldVersion
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/wim-web/ding/releases/latest" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"tag_name":"v9.9.9","assets":[]}`)
	}))
	defer server.Close()

	exePath := filepath.Join(t.TempDir(), "ding")
	if err := os.WriteFile(exePath, []byte("old ding binary"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	a, stdout, stderr := newTestApp(t, t.TempDir(), "", false, server.Client())
	a.releaseAPIBaseURL = server.URL
	a.executable = func() (string, error) { return exePath, nil }

	if code := a.run([]string{"update"}); code != exitOK {
		t.Fatalf("update exit = %d, stderr = %s", code, stderr.String())
	}
	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read executable: %v", err)
	}
	if string(got) != "old ding binary" {
		t.Fatalf("executable = %q, want unchanged", got)
	}
	if !strings.Contains(stdout.String(), "already up to date") {
		t.Fatalf("stdout = %q, want already current message", stdout.String())
	}
}

func makeReleaseArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	name := "ding"
	if runtime.GOOS == "windows" {
		name = "ding.exe"
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0o755,
		Size: int64(len(binary)),
	}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}
