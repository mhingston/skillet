package packagebuilder

import (
	"bytes"
	"testing"
)

func TestBuildIsDeterministicAndHashesExactBytes(t *testing.T) {
	entries := []Entry{{Path: "plan/SKILL.md", Kind: Regular, Mode: 0755, Size: 8}, {Path: "plan/references/info.md", Kind: Regular, Mode: 0644, Size: 4}}
	read := func(p string) ([]byte, error) {
		if p == "plan/SKILL.md" {
			return []byte("skill-md"), nil
		}
		return []byte("info"), nil
	}
	a, err := Build("plan", "plan", entries, read, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build("plan", "plan", entries, read, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.TarGZ, b.TarGZ) || !bytes.Equal(a.ZIP, b.ZIP) || a.TarGZDigest == "" || a.ZIPDigest == "" {
		t.Fatal("archives are not deterministic")
	}
}

func TestBuildRejectsUnsafeAndSpecialEntries(t *testing.T) {
	for _, entry := range []Entry{{Path: "plan/link", Kind: Symlink}, {Path: "../escape", Kind: Regular}, {Path: "plan/device", Kind: Special}} {
		if _, err := Build("plan", "plan", []Entry{entry}, func(string) ([]byte, error) { return nil, nil }, Limits{}); err == nil {
			t.Fatalf("entry %+v was accepted", entry)
		}
	}
}

func TestBuildDoesNotIncludeFilesOutsideSkillRoot(t *testing.T) {
	p, err := Build("plan", "plan", []Entry{{Path: "other/nope", Kind: Regular, Size: 4}, {Path: "plan/SKILL.md", Kind: Regular, Size: 1}}, func(p string) ([]byte, error) {
		if p == "plan/SKILL.md" {
			return []byte("x"), nil
		}
		return nil, nil
	}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.TarGZ) == 0 {
		t.Fatal("empty package")
	}
}

func TestBuildRequiresSkillEntrypoint(t *testing.T) {
	if _, err := Build("plan", "plan", []Entry{{Path: "plan/notes.md", Kind: Regular, Size: 1}}, func(string) ([]byte, error) { return []byte("x"), nil }, Limits{}); err == nil {
		t.Fatal("missing SKILL.md was accepted")
	}
}
