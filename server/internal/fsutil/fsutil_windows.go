//go:build windows

package fsutil

import (
	"io/fs"
	"strings"
	"syscall"
	"time"
)

// IsHidden reports whether the file has the Hidden or System attribute.
func IsHidden(fi fs.FileInfo) bool {
	if d, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		return d.FileAttributes&(syscall.FILE_ATTRIBUTE_HIDDEN|syscall.FILE_ATTRIBUTE_SYSTEM) != 0
	}
	return false
}

func createdTime(fi fs.FileInfo) (time.Time, bool) {
	if d, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		return time.Unix(0, d.CreationTime.Nanoseconds()), true
	}
	return time.Time{}, false
}

// LongPath adds the \\?\ prefix to absolute paths longer than 240 characters.
// Go's os package already does this internally; this is for paths passed to
// ffmpeg/ffprobe, which get them verbatim.
func LongPath(p string) string {
	if len(p) <= 240 || strings.HasPrefix(p, `\\?\`) {
		return p
	}
	if strings.HasPrefix(p, `\\`) {
		return `\\?\UNC\` + p[2:]
	}
	if len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return `\\?\` + strings.ReplaceAll(p, "/", `\`)
	}
	return p
}

// CaseInsensitive is true where file names compare case-insensitively.
const CaseInsensitive = true

// InUse reports whether another program has the file open for writing, e.g.
// a copy into the library that hasn't finished. It opens the file for reading
// while refusing write sharing, which fails only if a writer already has it.
func InUse(path string) bool {
	p, err := syscall.UTF16PtrFromString(LongPath(path))
	if err != nil {
		return false
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err == errSharingViolation
	}
	syscall.CloseHandle(h)
	return false
}

// ERROR_SHARING_VIOLATION (not exported by package syscall).
const errSharingViolation = syscall.Errno(32)
