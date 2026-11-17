package watch

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu   sync.Mutex
	seen []string
}

func (r *recorder) add(rel string) {
	r.mu.Lock()
	r.seen = append(r.seen, filepath.ToSlash(rel))
	r.mu.Unlock()
}

func (r *recorder) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, s := range r.seen {
			if s == want {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("no event for %q; got %q", want, r.seen)
}

func TestWatchNestedChangesAndRenames(t *testing.T) {
	old := pollEvery // macOS polls; check often enough for this test
	pollEvery = 50 * time.Millisecond
	defer func() { pollEvery = old }()
	root := t.TempDir()
	movie := filepath.Join(root, "Movie (2020)")
	if err := os.MkdirAll(filepath.Join(movie, "Subs"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &recorder{}
	done := make(chan struct{})
	go func() {
		Run(ctx, root, slog.New(slog.NewTextHandler(io.Discard, nil)), rec.add)
		close(done)
	}()
	time.Sleep(200 * time.Millisecond) // let the watch start

	if err := os.WriteFile(filepath.Join(movie, "Subs", "Movie (2020).ro.srt"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec.waitFor(t, "Movie (2020)/Subs/Movie (2020).ro.srt")

	// Watching must not lock folders: renaming a watched subfolder works.
	renamed := filepath.Join(root, "Movie Renamed (2020)")
	if err := os.Rename(movie, renamed); err != nil {
		t.Fatalf("rename while watching: %v", err)
	}
	rec.waitFor(t, "Movie Renamed (2020)")
	if err := os.RemoveAll(renamed); err != nil {
		t.Fatalf("delete while watching: %v", err)
	}
	rec.waitFor(t, "Movie Renamed (2020)")

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not stop")
	}
	for _, s := range rec.seen {
		if strings.HasPrefix(s, "..") {
			t.Errorf("path outside root reported: %q", s)
		}
	}
}
