//go:build !windows

package fsutil

import (
	"io/fs"
	"time"
)

// IsHidden is false on Unix; dot-files are handled by name.
func IsHidden(fi fs.FileInfo) bool { return false }

func createdTime(fi fs.FileInfo) (time.Time, bool) { return time.Time{}, false }

// LongPath is a no-op outside Windows.
func LongPath(p string) string { return p }

// CaseInsensitive is true where file names compare case-insensitively.
const CaseInsensitive = false

// InUse can't be detected cheaply on Unix (no mandatory sharing modes).
func InUse(path string) bool { return false }
