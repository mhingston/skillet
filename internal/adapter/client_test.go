package adapter

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDownloadAndExtractVerifiesAndInstallsSkill(t *testing.T) {
	archive := makeArchive(t, "plan", "---\nname: plan\n---\n# Plan\n")
	digest := sha256.Sum256(archive)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer ts.Close()
	destination := t.TempDir()
	entry, err := downloadAndExtract(t.Context(), ts.URL, hex.EncodeToString(digest[:]), "tar.gz", destination, "plan")
	if err != nil {
		t.Fatal(err)
	}
	if entry != filepath.Join(destination, "plan", "SKILL.md") {
		t.Fatalf("entrypoint = %q", entry)
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadAndExtractRejectsDigestMismatch(t *testing.T) {
	archive := makeArchive(t, "plan", "content")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer ts.Close()
	if _, err := downloadAndExtract(t.Context(), ts.URL, "0000000000000000000000000000000000000000000000000000000000000000", "tar.gz", t.TempDir(), "plan"); err == nil {
		t.Fatal("digest mismatch was accepted")
	}
}

func TestHostOSUsesSkilletContractNameOnMacOS(t *testing.T) {
	if runtime.GOOS == "darwin" && hostOS() != "macos" {
		t.Fatalf("hostOS() = %q, want macos", hostOS())
	}
}

func makeArchive(t *testing.T, name, content string) []byte {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "archive-*.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	b := []byte(content)
	if err := tw.WriteHeader(&tar.Header{Name: name + "/SKILL.md", Mode: 0600, Size: int64(len(b))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	return archive
}
