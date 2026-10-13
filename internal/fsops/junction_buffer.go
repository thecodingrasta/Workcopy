package fsops

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

// Windows mount-point reparse offsets are byte offsets from PathBuffer, not
// character counts. Keep encoding separate so its layout is tested on every OS.
func junctionBuffer(target string) ([]byte, error) {
	if len(target) < 3 || target[1] != ':' || target[2] != '\\' || strings.ContainsRune(target, 0) {
		return nil, fmt.Errorf("junction target must be an absolute local drive path")
	}
	substitute := append(utf16.Encode([]rune(`\??\`+target)), 0)
	printName := append(utf16.Encode([]rune(target)), 0)
	size := 16 + 2*(len(substitute)+len(printName))
	if size > 16384 {
		return nil, fmt.Errorf("junction target is too long")
	}
	body := make([]byte, size)
	binary.LittleEndian.PutUint32(body[0:4], 0xa0000003)
	binary.LittleEndian.PutUint16(body[4:6], uint16(size-8))
	binary.LittleEndian.PutUint16(body[10:12], uint16((len(substitute)-1)*2))
	binary.LittleEndian.PutUint16(body[12:14], uint16(len(substitute)*2))
	binary.LittleEndian.PutUint16(body[14:16], uint16((len(printName)-1)*2))
	for n, value := range append(substitute, printName...) {
		binary.LittleEndian.PutUint16(body[16+2*n:], value)
	}
	return body, nil
}
