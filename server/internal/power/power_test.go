package power

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestKeeper(t *testing.T) {
	now := time.Unix(1000, 0)
	var mu sync.Mutex
	var calls []bool
	k := &Keeper{log: slog.New(slog.NewTextHandler(io.Discard, nil)), now: func() time.Time { return now },
		wake: make(chan struct{}, 1), set: func(a bool) error { mu.Lock(); calls = append(calls, a); mu.Unlock(); return nil }}

	if k.wanted() {
		t.Fatal("awake before any activity")
	}
	end := k.Begin()
	k.apply(k.wanted())
	now = now.Add(time.Hour) // a long direct-play response still open
	if !k.wanted() {
		t.Error("must stay awake while a stream is open")
	}
	end()
	now = now.Add(Idle - time.Second)
	if !k.wanted() {
		t.Error("must stay awake until Idle after the stream")
	}
	now = now.Add(2 * time.Second)
	if k.wanted() {
		t.Error("should allow sleep after Idle")
	}
	k.apply(k.wanted())
	k.Touch() // a progress report
	if !k.wanted() {
		t.Error("touch should keep it awake")
	}
	k.apply(true)
	k.apply(true) // no repeated OS calls
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 || !calls[0] || calls[1] || !calls[2] {
		t.Errorf("OS calls = %v", calls)
	}
}

// The real OS call works (Windows) and Run releases it on shutdown.
func TestRun(t *testing.T) {
	k := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { k.Run(ctx); close(done) }()
	end := k.Begin()
	deadline := time.Now().Add(2 * time.Second)
	for k.set != nil && !k.Awake() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if k.set != nil && !k.Awake() {
		t.Error("not awake while streaming")
	}
	end()
	cancel()
	<-done
	if k.Awake() {
		t.Error("still awake after shutdown")
	}
}
