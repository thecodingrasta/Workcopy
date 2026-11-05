package eol

import (
	"bytes"
	"testing"
)

// The converter must be reversible on anything Workcopy would convert, and
// must never touch what it declines to convert, whatever the bytes are.
func FuzzConversionRoundTrip(f *testing.F) {
	for _, seed := range []string{"", "a", "\n", "a\nb\n", "a\r\nb", "\r", "\r\r\n", "é\n\x00\n", "a\n\n\nb", "\x1a", "line\rmac\nunix\r\n"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, blob []byte) {
		for _, rule := range []string{Raw, LF, CRLF, AutoLF, AutoCRLF} {
			token := ForBlob(rule, blob)
			lane := ToLane(blob, token)
			if token == Raw && !bytes.Equal(lane, blob) {
				t.Fatalf("a raw file must be written exactly: rule=%q %q -> %q", rule, blob, lane)
			}
			if token != Raw {
				if bytes.IndexByte(blob, '\r') >= 0 {
					t.Fatalf("a blob with a carriage return must stay raw: rule=%q %q", rule, blob)
				}
				if got := Normalize(lane); !bytes.Equal(got, blob) {
					t.Fatalf("not reversible: rule=%q token=%q %q -> %q -> %q", rule, token, blob, lane, got)
				}
			}
			if !Valid(token) {
				t.Fatalf("ForBlob produced a token that cannot be persisted: %q", token)
			}
			// Normalize is deterministic, so a file read twice hashes identically.
			if !bytes.Equal(Normalize(lane), Normalize(append([]byte(nil), lane...))) {
				t.Fatalf("Normalize is not deterministic for %q", lane)
			}
		}
	})
}
