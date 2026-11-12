//go:build linux

package storage

import "syscall"

// Filesystem magic numbers from statfs(2). They are fixed by the kernel ABI.
const (
	magicExt      = 0xEF53 // ext2, ext3 and ext4 share one value
	magicXFS      = 0x58465342
	magicBtrfs    = 0x9123683E
	magicTmpfs    = 0x01021994
	magicVFAT     = 0x4d44
	magicExFAT    = 0x2011BAB0
	magicOverlay  = 0x794c7630
	magicNFS      = 0x6969
	magicV9FS     = 0x01021997
	magicZFS      = 0x2fc12fc1
	magicFUSE     = 0x65735546
	magicF2FS     = 0xF2F52010
	magicBcachefs = 0xca451a4e
)

type knownFileSystem struct {
	name  string
	links Support
	clone Support
	why   string
}

var knownFileSystems = map[uint32]knownFileSystem{
	magicExt:      {"ext2/ext3/ext4", Yes, No, "ext2/3/4 has no block sharing"},
	magicXFS:      {"xfs", Yes, Unknown, "XFS shares blocks only when created with reflink enabled; a clone attempt is needed to know"},
	magicBtrfs:    {"btrfs", Yes, Yes, "btrfs shares blocks between files (reflink)"},
	magicTmpfs:    {"tmpfs", Yes, No, "tmpfs has no block sharing"},
	magicVFAT:     {"vfat", No, No, "FAT has neither hard links nor block sharing"},
	magicExFAT:    {"exfat", No, No, "exFAT has neither hard links nor block sharing"},
	magicOverlay:  {"overlayfs", Unknown, Unknown, "overlayfs behaviour depends on the underlying filesystems"},
	magicNFS:      {"nfs", Unknown, Unknown, "depends on the server"},
	magicV9FS:     {"9p", Unknown, Unknown, "a 9p share (for example a Windows drive seen from WSL); behaviour depends on the host"},
	magicZFS:      {"zfs", Yes, Unknown, "OpenZFS block cloning depends on the pool version and settings"},
	magicFUSE:     {"fuse", Unknown, Unknown, "depends on the FUSE filesystem"},
	magicF2FS:     {"f2fs", Yes, No, "f2fs has no block sharing"},
	magicBcachefs: {"bcachefs", Yes, Unknown, "a clone attempt is needed to know"},
}

func fileSystemInfo(path string) (name string, links, clone Support, basis string, free, total uint64, err error) {
	var stat syscall.Statfs_t
	if err = syscall.Statfs(path, &stat); err != nil {
		return
	}
	free, total = uint64(stat.Bavail)*uint64(stat.Bsize), uint64(stat.Blocks)*uint64(stat.Bsize)
	known, ok := knownFileSystems[uint32(stat.Type)]
	if !ok {
		return "unrecognised", Unknown, Unknown, "filesystem type is not in Workcopy's table", free, total, nil
	}
	return known.name, known.links, known.clone, known.why, free, total, nil
}
