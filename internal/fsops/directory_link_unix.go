//go:build linux || darwin

package fsops

const DirectoryLinkStrategy = "symlink"

func (r *Root) ReplaceDirectoryLink(name, target string) error {
	return r.ReplaceSymlink(name, target, true)
}
