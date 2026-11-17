package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "minimedia.log")
	w, err := Open(path, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 39) + "\n" // 40 bytes: two lines per file
	for i := 0; i < 11; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	for name, want := range map[string]int64{"": 40, ".1": 80, ".2": 80, ".3": 80, ".4": -1} {
		fi, err := os.Stat(path + name)
		if want < 0 {
			if err == nil {
				t.Errorf("%s exists, only 3 old files should be kept", name)
			}
			continue
		}
		if err != nil || fi.Size() != want {
			t.Errorf("log%s: %v, size %v, want %d", name, err, fi, want)
		}
	}
	// Reopening appends to the existing file.
	w, _ = Open(path, 100, 3)
	w.Write([]byte(line))
	w.Close()
	if fi, _ := os.Stat(path); fi.Size() != 80 {
		t.Errorf("after reopen: %d bytes", fi.Size())
	}
}
