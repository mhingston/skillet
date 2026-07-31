package packagestore

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPutGetIsIdempotentAndContentAddressed(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "packages"))
	data := []byte("immutable package")
	digest, err := s.Put("tar.gz", data)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Put("tar.gz", data)
	if err != nil || again != digest {
		t.Fatalf("second put = %q, %v", again, err)
	}
	got, err := s.Get("tar.gz", digest)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("get = %q, %v", got, err)
	}
}

func TestGetDetectsCorruption(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "packages"))
	digest, err := s.Put("zip", []byte("good"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCorrupt(s.Root, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("zip", digest); err == nil {
		t.Fatal("corruption was accepted")
	}
}

func TestGetRejectsFormatAndDigestTraversal(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "packages"))
	for _, args := range [][2]string{{"../../outside", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, {"zip", "../../outside"}, {"zip", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}} {
		if _, err := s.Get(args[0], args[1]); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestOpenReturnsExactPackageSize(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "packages"))
	data := []byte("streamed")
	digest, err := s.Put("zip", data)
	if err != nil {
		t.Fatal(err)
	}
	f, size, err := s.Open("zip", digest)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if size != int64(len(data)) {
		t.Fatalf("size=%d", size)
	}
}

func writeCorrupt(root, digest string) error {
	return os.WriteFile(filepath.Join(root, "sha256", digest[:2], digest+".zip"), []byte("bad"), 0600)
}
