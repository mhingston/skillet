// Package packagebuilder creates deterministic archives from validated Git-tree
// entries. It never follows links or executes repository content.
package packagebuilder

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/mhingston/skillet/internal/skillspec"
)

type Kind uint8

const (
	Regular Kind = iota + 1
	Directory
	Symlink
	Special
)

type Entry struct {
	Path string
	Kind Kind
	Mode int64
	Size int64
}
type Limits struct {
	MaxFiles                                     int
	MaxFileBytes, MaxTotalBytes, MaxArchiveBytes int64
}
type Reader func(string) ([]byte, error)
type Package struct {
	TarGZ, ZIP             []byte
	TarGZDigest, ZIPDigest string
}

func Build(skillName, root string, entries []Entry, read Reader, limits Limits) (Package, error) {
	if read == nil {
		return Package{}, fmt.Errorf("package reader is required")
	}
	if limits.MaxFiles == 0 {
		limits.MaxFiles = 1000
	}
	if limits.MaxFileBytes == 0 {
		limits.MaxFileBytes = 25 << 20
	}
	if limits.MaxTotalBytes == 0 {
		limits.MaxTotalBytes = 100 << 20
	}
	if limits.MaxArchiveBytes == 0 {
		limits.MaxArchiveBytes = 50 << 20
	}
	if !skillspec.ValidName(skillName) || path.Base(skillName) != skillName || strings.Contains(skillName, "/") {
		return Package{}, fmt.Errorf("invalid skill package name %q", skillName)
	}
	root = path.Clean(root)
	if root == "." || root == ".." || strings.HasPrefix(root, "../") || strings.HasPrefix(root, "/") {
		return Package{}, fmt.Errorf("invalid skill root %q", root)
	}
	files := make([]Entry, 0, len(entries))
	seen := map[string]bool{}
	var total int64
	for _, entry := range entries {
		clean := path.Clean(entry.Path)
		if clean != entry.Path || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
			return Package{}, fmt.Errorf("unsafe package path %q", entry.Path)
		}
		if seen[clean] {
			return Package{}, fmt.Errorf("duplicate package path %q", clean)
		}
		seen[clean] = true
		if entry.Kind == Symlink || entry.Kind == Special {
			return Package{}, fmt.Errorf("unsupported package entry %q", clean)
		}
		if entry.Kind != Regular && entry.Kind != Directory {
			return Package{}, fmt.Errorf("unknown package entry %q", clean)
		}
		if clean != root && !strings.HasPrefix(clean, root+"/") {
			continue
		}
		if entry.Kind == Regular {
			if len(files) >= limits.MaxFiles {
				return Package{}, fmt.Errorf("package exceeds maximum file count")
			}
			if entry.Size > limits.MaxFileBytes {
				return Package{}, fmt.Errorf("file %q exceeds maximum size", clean)
			}
			files = append(files, entry)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	requiredEntrypoint := root + "/SKILL.md"
	foundEntrypoint := false
	for _, entry := range files {
		if entry.Path == requiredEntrypoint {
			foundEntrypoint = true
			break
		}
	}
	if !foundEntrypoint {
		return Package{}, fmt.Errorf("package must contain %s", requiredEntrypoint)
	}
	contents := make(map[string][]byte, len(files))
	for _, entry := range files {
		b, err := read(entry.Path)
		if err != nil {
			return Package{}, fmt.Errorf("read %s: %w", entry.Path, err)
		}
		if int64(len(b)) > limits.MaxFileBytes {
			return Package{}, fmt.Errorf("file %q exceeds maximum size", entry.Path)
		}
		total += int64(len(b))
		if total > limits.MaxTotalBytes {
			return Package{}, fmt.Errorf("package exceeds maximum uncompressed size")
		}
		contents[entry.Path] = b
	}
	tarBytes, err := buildTarGZ(skillName, root, files, contents)
	if err != nil {
		return Package{}, err
	}
	zipBytes, err := buildZIP(skillName, root, files, contents)
	if err != nil {
		return Package{}, err
	}
	if int64(len(tarBytes)) > limits.MaxArchiveBytes || int64(len(zipBytes)) > limits.MaxArchiveBytes {
		return Package{}, fmt.Errorf("package exceeds maximum archive size")
	}
	return Package{TarGZ: tarBytes, ZIP: zipBytes, TarGZDigest: digest(tarBytes), ZIPDigest: digest(zipBytes)}, nil
}

func packagePath(skillName, root, entry string) string {
	rel := strings.TrimPrefix(entry, root)
	rel = strings.TrimPrefix(rel, "/")
	return skillName + "/" + rel
}
func buildTarGZ(skillName, root string, files []Entry, contents map[string][]byte) ([]byte, error) {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	gz.Header.ModTime = time.Unix(0, 0)
	gz.Header.Name = ""
	tw := tar.NewWriter(gz)
	for _, entry := range files {
		b := contents[entry.Path]
		h := &tar.Header{Name: packagePath(skillName, root, entry.Path), Mode: entry.Mode&0111 | 0644, Size: int64(len(b)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Uid: 0, Gid: 0, Uname: "", Gname: ""}
		if err := tw.WriteHeader(h); err != nil {
			return nil, err
		}
		if _, err := tw.Write(b); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func buildZIP(skillName, root string, files []Entry, contents map[string][]byte) ([]byte, error) {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, entry := range files {
		h := &zip.FileHeader{Name: packagePath(skillName, root, entry.Path), Method: zip.Deflate}
		h.SetModTime(time.Unix(0, 0))
		h.SetMode(fs.FileMode(0100000 | (entry.Mode & 0777)))
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(w, bytes.NewReader(contents[entry.Path])); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func digest(b []byte) string { sum := sha256.Sum256(b); return fmt.Sprintf("%x", sum[:]) }
