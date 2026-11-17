// Package power keeps the PC from going to sleep while the TV is playing
// (PLAN.md 7): from the first stream request until 2 minutes after the last
// one, with any stream response still open counting as activity.
package power

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"time"
)

// Idle is how long after the last streaming activity sleep is allowed again.
const Idle = 2 * time.Minute

// Keeper tracks streaming activity.
type Keeper struct {
	log  *slog.Logger
	set  func(awake bool) error // the OS call; nil = not supported here
	now  func() time.Time
	wake chan struct{}

	mu     sync.Mutex
	active int       // stream responses in progress
	last   time.Time // last activity
	awake  bool      // what was last requested from the OS
}

// New returns a Keeper using the OS's sleep control (a no-op where there is none).
func New(log *slog.Logger) *Keeper {
	return &Keeper{log: log, set: setAwake, now: time.Now, wake: make(chan struct{}, 1)}
}

// Begin marks a stream response as started; call the returned func when it ends.
func (k *Keeper) Begin() func() {
	k.mu.Lock()
	k.active++
	k.last = k.now()
	k.mu.Unlock()
	k.poke()
	return func() {
		k.mu.Lock()
		k.active--
		k.last = k.now()
		k.mu.Unlock()
		k.poke()
	}
}

// Touch records activity that isn't a stream (e.g. a progress report).
func (k *Keeper) Touch() {
	k.mu.Lock()
	k.last = k.now()
	k.mu.Unlock()
	k.poke()
}

func (k *Keeper) poke() {
	select {
	case k.wake <- struct{}{}:
	default:
	}
}

// wanted reports whether the PC should stay awake now.
func (k *Keeper) wanted() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.active > 0 || (!k.last.IsZero() && k.now().Sub(k.last) < Idle)
}

// Awake reports whether sleep is currently being held off.
func (k *Keeper) Awake() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.awake
}

// Run applies the state until ctx ends. The OS state belongs to a thread, so
// one goroutine locked to its thread makes every call.
func (k *Keeper) Run(ctx context.Context) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	defer k.apply(false)
	for {
		k.apply(k.wanted())
		select {
		case <-ctx.Done():
			return
		case <-k.wake:
		case <-tick.C:
		}
	}
}

func (k *Keeper) apply(want bool) {
	k.mu.Lock()
	changed := want != k.awake
	k.awake = want
	k.mu.Unlock()
	if !changed || k.set == nil {
		return
	}
	if err := k.set(want); err != nil {
		k.log.Warn("cannot change the sleep setting", "awake", want, "err", err)
		return
	}
	if want {
		k.log.Info("streaming: keeping the PC awake")
	} else {
		k.log.Info("no streaming for a while: the PC may sleep again")
	}
}
