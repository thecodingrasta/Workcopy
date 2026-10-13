package model

import "testing"

func TestStrictJSONRejectsUnknownAndTrailingData(t *testing.T) {
	for _, s := range []string{`{"schemaVersion":1,"typo":true}`, `{"schemaVersion":1} {}`} {
		var c Config
		if DecodeStrict([]byte(s), &c) == nil {
			t.Fatal("accepted invalid config", s)
		}
	}
}
func TestVersionIdentityIsContentAndMode(t *testing.T) {
	a := Version(Regular, 0644, []byte("x"))
	b := a
	b.ObjectID = "some-git-id"
	if !a.Equal(b) {
		t.Fatal("provenance altered content equality")
	}
	b.Mode = 0755
	if a.Equal(b) {
		t.Fatal("mode change ignored")
	}
	if Version(Missing, 0, nil).Exists() {
		t.Fatal("missing exists")
	}
}

func TestStrictJSONRejectsAmbiguousObjects(t *testing.T) {
	for _, body := range []string{`null`, `{"schemaVersion":1,"schemaVersion":2}`, `{"schemaVersion":1,"profiles":{"daily":{"include":["a"],"include":["b"]}}}`} {
		var config Config
		if err := DecodeStrict([]byte(body), &config); err == nil {
			t.Errorf("accepted ambiguous document: %s", body)
		}
	}
}
