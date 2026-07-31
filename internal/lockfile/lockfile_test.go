package lockfile

import (
	"bytes"
	"testing"
)

func TestMarshalIsDeterministicAndSortsSkills(t *testing.T) {
	f := New()
	f.Skills["z"] = Entry{Name: "z"}
	f.Skills["a"] = Entry{Name: "a"}
	a, e := f.Marshal()
	if e != nil {
		t.Fatal(e)
	}
	b, e := f.Marshal()
	if e != nil || !bytes.Equal(a, b) {
		t.Fatal("lockfile is not stable")
	}
	if bytes.Index(a, []byte(`"a"`)) > bytes.Index(a, []byte(`"z"`)) {
		t.Fatalf("skills are not sorted: %s", a)
	}
}
