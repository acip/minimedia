package watch

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPollTree(t *testing.T) {
	old := pollEvery
	pollEvery = 20 * time.Millisecond
	defer func() { pollEvery = old }()

	root := t.TempDir()
	write := func(rel, data string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Sintel (2010)/Sintel (2010).mkv", "a")

	var mu sync.Mutex
	seen := map[string]bool{}
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- pollTree(ctx, root, func(rel string) {
			mu.Lock()
			seen[filepath.ToSlash(rel)] = true
			mu.Unlock()
		}, func() { close(ready) })
	}()
	<-ready
	expect := func(rel string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			ok := seen[rel]
			delete(seen, rel)
			mu.Unlock()
			if ok {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Errorf("no change reported for %q", rel)
	}

	write("Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv", "b")
	expect("Big Buck Bunny (2008)")
	expect("Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv")

	write("Sintel (2010)/Sintel (2010).mkv", "grown") // a copy still in progress
	expect("Sintel (2010)/Sintel (2010).mkv")

	if err := os.Remove(filepath.Join(root, "Sintel (2010)", "Sintel (2010).mkv")); err != nil {
		t.Fatal(err)
	}
	expect("Sintel (2010)/Sintel (2010).mkv")

	write(".hidden/x.mkv", "x")
	write("Big Buck Bunny (2008)/Extras/trailer.mkv", "x")
	time.Sleep(10 * pollEvery)
	mu.Lock()
	for rel := range seen {
		if rel != "Big Buck Bunny (2008)" { // its folder time changed
			t.Errorf("unexpected change %q", rel)
		}
	}
	mu.Unlock()

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("removing the root should stop polling with an error")
		}
	case <-time.After(2 * time.Second):
		t.Error("polling didn't notice the root was removed")
	}
	cancel()
}
