package packagestore

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

type Store struct{ Root string }

func New(root string) *Store { return &Store{Root: root} }
func (s *Store) Put(format string, data []byte) (string, error) {
	if format != "tar.gz" && format != "zip" {
		return "", fmt.Errorf("unsupported package format %q", format)
	}
	sum := sha256.Sum256(data)
	digest := fmt.Sprintf("%x", sum[:])
	dir := filepath.Join(s.Root, "sha256", digest[:2])
	final := filepath.Join(dir, digest+"."+format)
	if existing, err := os.ReadFile(final); err == nil {
		if stringHash(existing) != digest {
			return "", fmt.Errorf("existing package digest mismatch")
		}
		return digest, nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".staging-")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(name, final); err != nil {
		existing, readErr := os.ReadFile(final)
		if readErr == nil && stringHash(existing) == digest {
			return digest, nil
		}
		return "", err
	}
	return digest, nil
}
func (s *Store) Get(format, digest string) ([]byte, error) {
	if format != "tar.gz" && format != "zip" {
		return nil, fmt.Errorf("unsupported package format %q", format)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(digest) {
		return nil, fmt.Errorf("invalid digest")
	}
	b, err := os.ReadFile(filepath.Join(s.Root, "sha256", digest[:2], digest+"."+format))
	if err != nil {
		return nil, err
	}
	if stringHash(b) != digest {
		return nil, fmt.Errorf("package integrity mismatch")
	}
	return b, nil
}
func (s *Store) Open(format, digest string) (io.ReadCloser, int64, error) {
	if format != "tar.gz" && format != "zip" {
		return nil, 0, fmt.Errorf("unsupported package format %q", format)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(digest) {
		return nil, 0, fmt.Errorf("invalid digest")
	}
	f, err := os.Open(filepath.Join(s.Root, "sha256", digest[:2], digest+"."+format))
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, 0, fmt.Errorf("package is not a regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		f.Close()
		return nil, 0, err
	}
	if fmt.Sprintf("%x", hash.Sum(nil)) != digest {
		f.Close()
		return nil, 0, fmt.Errorf("package integrity mismatch")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}
func stringHash(b []byte) string { sum := sha256.Sum256(b); return fmt.Sprintf("%x", sum[:]) }
