//go:build windows

package fsutil

import (
	"strings"
	"testing"
)

func TestLongPath(t *testing.T) {
	short := `C:\Media\Movies\Film.mkv`
	if LongPath(short) != short {
		t.Errorf("short path changed: %q", LongPath(short))
	}
	long := `D:\Media\` + strings.Repeat("Știința Ușoară ", 20) + `\film.mkv`
	if got := LongPath(long); got != `\\?\`+long {
		t.Errorf("drive path = %q", got)
	}
	unc := `\\nas\media\` + strings.Repeat("x", 250) + `\f.mkv`
	if got := LongPath(unc); got != `\\?\UNC\nas\media\`+strings.Repeat("x", 250)+`\f.mkv` {
		t.Errorf("unc path = %q", got)
	}
	if LongPath(`\\?\`+long) != `\\?\`+long {
		t.Error("already-prefixed path changed")
	}
}
