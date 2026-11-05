// Package eol mirrors Git's line-ending rules (core.autocrlf, core.eol and the
// text, eol and crlf attributes) so a lane can use the same working-tree form
// as the repository's own checkout while Workcopy hashes, compares and merges
// the normalised (LF) form that Git stores.
//
// The rules are a port of Git's convert.c decision table, limited to line
// endings: external filters, working-tree-encoding and ident are rejected
// elsewhere and are never executed here. parity_test.go compares every case
// against a real git checkout, so a Git change that alters the table fails the
// test rather than silently diverging.
package eol

import "bytes"

// Policy tokens. Raw is the zero value and means "do not convert". LF and CRLF
// are persisted on file versions: the file is text, is normalised when read,
// and is written in that form. AutoLF and AutoCRLF only describe a rule whose
// outcome depends on the file's content; they are resolved before persisting.
const (
	Raw      = ""
	LF       = "lf"
	CRLF     = "crlf"
	AutoLF   = "auto-lf"
	AutoCRLF = "auto-crlf"
)

// Attributes are the values git check-attr reports for one path: "set",
// "unset", "unspecified" or a string value. The zero value means unspecified.
type Attributes struct {
	Text, CRLF, EOL string
}

// Config carries the repository's effective core.* settings.
type Config struct {
	// AutoCRLF is "true", "input" or "false" (core.autocrlf).
	AutoCRLF string
	// EOL is "lf", "crlf" or "native" (core.eol).
	EOL string
	// NativeCRLF reports whether "native" means CRLF for the Git in use, which
	// is true for Windows builds of Git.
	NativeCRLF bool
}

type action int

const (
	undefined action = iota
	binary
	text
	textInput
	textCRLF
	auto
	autoInput
	autoCRLF
)

func attrAction(value string) action {
	switch value {
	case "set":
		return text
	case "unset":
		return binary
	case "", "unspecified":
		return undefined
	case "input":
		return textInput
	case "auto":
		return auto
	}
	return undefined
}

// textEOLIsCRLF is Git's text_eol_is_crlf.
func (c Config) textEOLIsCRLF() bool {
	switch c.AutoCRLF {
	case "true":
		return true
	case "input":
		return false
	}
	switch c.EOL {
	case "crlf":
		return true
	case "lf":
		return false
	}
	return c.NativeCRLF
}

// Resolve returns the policy token Git's checkout would apply to a path with
// these attributes under this configuration.
func Resolve(a Attributes, c Config) string {
	chosen := attrAction(a.Text)
	if chosen == undefined {
		chosen = attrAction(a.CRLF)
	}
	if chosen != binary {
		switch {
		case chosen == auto && a.EOL == "lf":
			chosen = autoInput
		case chosen == auto && a.EOL == "crlf":
			chosen = autoCRLF
		case a.EOL == "lf":
			chosen = textInput
		case a.EOL == "crlf":
			chosen = textCRLF
		}
	}
	if chosen == text {
		if c.textEOLIsCRLF() {
			chosen = textCRLF
		} else {
			chosen = textInput
		}
	}
	if chosen == undefined {
		switch c.AutoCRLF {
		case "true":
			chosen = autoCRLF
		case "input":
			chosen = autoInput
		default:
			chosen = binary
		}
	}
	switch chosen {
	case textInput:
		return LF
	case textCRLF:
		return CRLF
	case autoInput:
		return AutoLF
	case autoCRLF:
		return AutoCRLF
	case auto:
		if c.textEOLIsCRLF() {
			return AutoCRLF
		}
		return AutoLF
	}
	return Raw
}

type stats struct {
	nul, lonecr, crlf, lonelf, printable, nonprintable int
}

// gather is Git's gather_stats.
func gather(body []byte) stats {
	var s stats
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '\r':
			if i+1 < len(body) && body[i+1] == '\n' {
				s.crlf++
				i++
			} else {
				s.lonecr++
			}
		case c == '\n':
			s.lonelf++
		case c == 0x7f:
			s.nonprintable++
		case c < 32:
			switch c {
			case '\b', '\t', 033, '\f':
				s.printable++
			case 0:
				s.nul++
				s.nonprintable++
			default:
				s.nonprintable++
			}
		default:
			s.printable++
		}
	}
	// A trailing DOS EOF marker is not counted as non-printable.
	if len(body) >= 1 && body[len(body)-1] == 032 {
		s.nonprintable--
	}
	return s
}

// looksBinary is Git's convert_is_binary.
func looksBinary(s stats) bool {
	return s.lonecr > 0 || s.nul > 0 || (s.printable>>7) < s.nonprintable
}

func resolvedToken(token string) string {
	switch token {
	case AutoLF:
		return LF
	case AutoCRLF:
		return CRLF
	}
	return token
}

// ForBlob decides the persisted token for a committed blob. A blob that
// already contains a carriage return is left raw: Git does not convert such
// content on checkout, and converting it could not be reversed exactly.
// Detecting that case here keeps the lane's bytes identical to the blob so a
// fresh lane never reports a phantom change.
func ForBlob(token string, blob []byte) string {
	if token == Raw || bytes.IndexByte(blob, '\r') >= 0 {
		return Raw
	}
	if (token == AutoLF || token == AutoCRLF) && looksBinary(gather(blob)) {
		return Raw
	}
	return resolvedToken(token)
}

// ForLocal decides the token for a file that is not in the baseline, from the
// content currently on disk. Content-dependent rules apply Git's text
// detection to what the user wrote.
func ForLocal(token string, body []byte) string {
	if token == Raw {
		return Raw
	}
	if (token == AutoLF || token == AutoCRLF) && looksBinary(gather(body)) {
		return Raw
	}
	return resolvedToken(token)
}

// Normalize returns the stored (LF) form of text read from a lane.
func Normalize(body []byte) []byte {
	if bytes.IndexByte(body, '\r') < 0 {
		return body
	}
	return bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
}

// ToLane returns the bytes to write for stored content under a token.
func ToLane(body []byte, token string) []byte {
	if token != CRLF || bytes.IndexByte(body, '\n') < 0 {
		return body
	}
	out := make([]byte, 0, len(body)+bytes.Count(body, []byte{'\n'}))
	for i, c := range body {
		if c == '\n' && (i == 0 || body[i-1] != '\r') {
			out = append(out, '\r')
		}
		out = append(out, c)
	}
	return out
}

// Valid reports whether a token may be persisted on a file version.
func Valid(token string) bool { return token == Raw || token == LF || token == CRLF }
