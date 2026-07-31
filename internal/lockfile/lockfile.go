package lockfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

type Entry struct {
	Name      string    `json:"name"`
	Source    Source    `json:"source"`
	Resolved  Resolved  `json:"resolved"`
	Integrity Integrity `json:"integrity"`
}
type Source struct {
	Type          string `json:"type"`
	RepositoryID  string `json:"repositoryId"`
	RepositoryURL string `json:"repositoryUrl"`
	Path          string `json:"path"`
}
type Resolved struct {
	Commit string `json:"commit"`
	Tree   string `json:"tree"`
}
type Integrity struct {
	Algorithm string `json:"algorithm"`
	Archive   string `json:"archive"`
	Format    string `json:"format"`
}
type File struct {
	LockfileVersion int              `json:"lockfileVersion"`
	Skills          map[string]Entry `json:"skills"`
}

func New() File { return File{LockfileVersion: 1, Skills: map[string]Entry{}} }
func (f File) Marshal() ([]byte, error) {
	if f.LockfileVersion == 0 {
		f.LockfileVersion = 1
	}
	if f.Skills == nil {
		f.Skills = map[string]Entry{}
	}
	keys := make([]string, 0, len(f.Skills))
	for k := range f.Skills {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := struct {
		LockfileVersion int              `json:"lockfileVersion"`
		Skills          map[string]Entry `json:"skills"`
	}{f.LockfileVersion, map[string]Entry{}}
	for _, k := range keys {
		ordered.Skills[k] = f.Skills[k]
	}
	b, e := json.MarshalIndent(ordered, "", "  ")
	if e != nil {
		return nil, e
	}
	return append(b, '\n'), nil
}
func (f File) Write(path string) error {
	b, e := f.Marshal()
	if e != nil {
		return e
	}
	return os.WriteFile(path, b, 0600)
}
func Parse(b []byte) (File, error) {
	var f File
	if err := json.Unmarshal(bytes.TrimSpace(b), &f); err != nil {
		return File{}, fmt.Errorf("parse lockfile: %w", err)
	}
	if f.LockfileVersion != 1 {
		return File{}, fmt.Errorf("unsupported lockfile version %d", f.LockfileVersion)
	}
	return f, nil
}
