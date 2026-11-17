package progress

import (
	"testing"
	"time"
)

const min = 60 * 1000

func TestThresholds(t *testing.T) {
	cases := []struct {
		name            string
		pos, dur, cred  int64
		watched, resume bool
	}{
		{"start", 30 * 1000, 100 * min, 0, false, false},
		{"just under 2 min", 2*min - 1, 100 * min, 0, false, false},
		{"exactly 2 min, long film", 2 * min, 60 * min, 0, false, true},
		{"2 min but under 3% of 120 min", 3 * min, 120 * min, 0, false, false},
		{"3% of 120 min", 3.6 * min, 120 * min, 0, false, true},
		{"middle", 50 * min, 100 * min, 0, false, true},
		{"just under 90%", 90*min - 1, 100 * min, 0, false, true},
		{"exactly 90%", 90 * min, 100 * min, 0, true, false},
		{"end", 100 * min, 100 * min, 0, true, false},
		{"past credits marker", 86 * min, 100 * min, 85 * min, true, false},
		{"before credits marker", 84 * min, 100 * min, 85 * min, false, true},
		{"credits marker beyond duration ignored", 80 * min, 100 * min, 120 * min, false, true},
		{"short episode, 2.5 of 3 min", 150 * 1000, 3 * min, 0, false, true},
		{"short episode at credits", 150 * 1000, 3 * min, 150 * 1000, true, false},
		{"unknown duration", 50 * min, 0, 0, false, true},
	}
	for _, c := range cases {
		w := IsWatchedPosition(c.pos, c.dur, c.cred)
		r := !w && IsResumable(c.pos, c.dur)
		if w != c.watched || r != c.resume {
			t.Errorf("%s: watched=%v resumable=%v, want %v %v", c.name, w, r, c.watched, c.resume)
		}
	}
}

func TestApplyCountsOncePerViewing(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 20, 0, 0, 0, time.UTC)
	dur := int64(100 * min)
	s := State{}

	s = Apply(s, 30*1000, dur, 0, t0)
	if s.PositionMs != 0 || s.Watched || s.DurationMs != dur {
		t.Fatalf("early report: %+v", s)
	}
	s = Apply(s, 40*min, dur, 0, t0.Add(40*time.Minute))
	if s.PositionMs != 40*min || s.Watched {
		t.Fatalf("mid report: %+v", s)
	}
	s = Apply(s, 91*min, dur, 0, t0.Add(91*time.Minute))
	if !s.Watched || s.PositionMs != 0 || s.PlayCount != 1 {
		t.Fatalf("crossing 90%%: %+v", s)
	}
	// Reports while the credits roll must not count again.
	s = Apply(s, 95*min, dur, 0, t0.Add(95*time.Minute))
	s = Apply(s, 99*min, dur, 0, t0.Add(99*time.Minute))
	if s.PlayCount != 1 || s.PositionMs != 0 {
		t.Fatalf("credits reports: %+v", s)
	}

	// A re-watch the next day: progress is stored, the checkmark stays.
	t1 := t0.Add(24 * time.Hour)
	s = Apply(s, 30*min, dur, 0, t1)
	if !s.Watched || s.PositionMs != 30*min {
		t.Fatalf("re-watch mid: %+v", s)
	}
	s = Apply(s, 92*min, dur, 0, t1.Add(62*time.Minute))
	if s.PlayCount != 2 || s.PositionMs != 0 {
		t.Fatalf("re-watch end: %+v", s)
	}

	// Jumping straight to the end in a new session also counts.
	s = Apply(s, 95*min, dur, 0, t1.Add(48*time.Hour))
	if s.PlayCount != 3 {
		t.Fatalf("new session end: %+v", s)
	}
}

func TestApplyKeepsDurationAndClampsNegative(t *testing.T) {
	s := Apply(State{DurationMs: 10 * min}, -5, 0, 0, time.Now())
	if s.DurationMs != 10*min || s.PositionMs != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestMarkWatchedUnwatched(t *testing.T) {
	now := time.Now()
	s := MarkWatched(State{PositionMs: 40 * min}, now)
	if !s.Watched || s.PlayCount != 1 || s.PositionMs != 0 {
		t.Fatalf("%+v", s)
	}
	s = MarkWatched(s, now) // idempotent
	if s.PlayCount != 1 {
		t.Fatalf("double mark counted twice: %+v", s)
	}
	s = MarkUnwatched(s)
	if s.Watched || s.PositionMs != 0 || s.PlayCount != 1 {
		t.Fatalf("%+v", s)
	}
}
