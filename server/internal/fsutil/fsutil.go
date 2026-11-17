// Package fsutil has the small OS-specific file helpers the scanner and the
// streaming endpoint need.
package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Open opens a file for reading, handling Windows long paths.
func Open(path string) (*os.File, error) { return os.Open(LongPath(path)) }

// Stat stats a file, handling Windows long paths.
func Stat(path string) (fs.FileInfo, error) { return os.Stat(LongPath(path)) }

// AddedTime is the best "date added" for a file: its creation time where the OS
// records one (copying a file on Windows keeps mtime but sets a new creation
// time), otherwise its modification time. Never in the future.
func AddedTime(fi fs.FileInfo) time.Time {
	t := fi.ModTime()
	if c, ok := createdTime(fi); ok && c.After(t) {
		t = c
	}
	if now := time.Now(); t.After(now) {
		t = now
	}
	return t
}

// Abs returns a clean absolute path.
func Abs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return filepath.Clean(p)
}
