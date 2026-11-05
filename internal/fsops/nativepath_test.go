package fsops

import (
	"errors"
	"strings"
	"testing"

	"workcopy/internal/model"
)

func TestCheckNativePathNamesTheRightFix(t *testing.T) {
	cases := []struct {
		goos, path, code, advice string
	}{
		{"windows", "/mnt/c/Users/me/repo", "posix-path-on-windows", "Windows path such as"},
		{"windows", "/home/me/repo", "posix-path-on-windows", "wslpath -w"},
		{"windows", `C:\Users\me\repo`, "", ""},
		{"windows", `C:/Users/me/repo`, "", ""},
		{"windows", `\\server\share\repo`, "", ""},
		{"windows", "//server/share/repo", "", ""},
		{"windows", `relative\path`, "", ""},
		{"linux", `C:\Users\me\repo`, "windows-path-on-posix", "wslpath -u"},
		{"linux", `c:/Users/me/repo`, "windows-path-on-posix", "/mnt/c/"},
		{"darwin", `\\server\share`, "windows-path-on-posix", "POSIX path"},
		{"linux", "/mnt/c/Users/me/repo", "", ""},
		{"linux", "relative/path", "", ""},
		{"linux", "/home/me/a:b", "", ""},
	}
	for _, c := range cases {
		err := checkNativePath(c.path, c.goos)
		if c.code == "" {
			if err != nil {
				t.Errorf("%s %q rejected: %v", c.goos, c.path, err)
			}
			continue
		}
		var failure *model.Failure
		if !errors.As(err, &failure) || failure.Code != c.code || failure.Exit != 2 || !strings.Contains(failure.Message, c.advice) {
			t.Errorf("%s %q: want usage failure %s mentioning %q, got %v", c.goos, c.path, c.code, c.advice, err)
		}
	}
}
