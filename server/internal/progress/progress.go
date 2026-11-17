// Package progress implements the watched/resume rules of PLAN.md section 4.5.
package progress

import "time"

const (
	WatchedFraction  = 0.90
	MinResumeMs      = 2 * 60 * 1000
	MinResumeFrac    = 0.03
	NewSessionGap    = 6 * time.Hour // a report after this gap counts as a new viewing
	DefaultCreditsMs = 30 * 1000
)

// State is what is stored per item.
type State struct {
	PositionMs   int64
	DurationMs   int64
	Watched      bool
	PlayCount    int
	LastPlayedAt time.Time
}

// Apply folds one progress report into the stored state.
//
//   - position ≥ 90% of duration, or past the credits marker (creditsStartMs > 0),
//     marks the item watched, resets the position to 0 and increments PlayCount —
//     once per viewing, not on every report sent while the credits roll;
//   - a position under 2 minutes or under 3% is not kept as resumable;
//   - anything else is stored as the resume position (Watched is left as is, so
//     re-watching a watched film keeps its checkmark).
func Apply(prev State, positionMs, durationMs, creditsStartMs int64, now time.Time) State {
	next := prev
	if durationMs > 0 {
		next.DurationMs = durationMs
	}
	dur := next.DurationMs
	if positionMs < 0 {
		positionMs = 0
	}
	next.LastPlayedAt = now

	if IsWatchedPosition(positionMs, dur, creditsStartMs) {
		// Still in the same viewing that already completed: nothing new to count.
		sameViewing := prev.Watched && prev.PositionMs == 0 && !prev.LastPlayedAt.IsZero() &&
			now.Sub(prev.LastPlayedAt) < NewSessionGap
		if !sameViewing {
			next.PlayCount++
		}
		next.Watched = true
		next.PositionMs = 0
		return next
	}
	if IsResumable(positionMs, dur) {
		next.PositionMs = positionMs
	} else {
		next.PositionMs = 0
	}
	return next
}

// IsWatchedPosition reports whether a position counts as having finished the item.
func IsWatchedPosition(positionMs, durationMs, creditsStartMs int64) bool {
	if durationMs <= 0 {
		return false
	}
	if float64(positionMs) >= WatchedFraction*float64(durationMs) {
		return true
	}
	return creditsStartMs > 0 && creditsStartMs < durationMs && positionMs >= creditsStartMs
}

// IsResumable reports whether a position is worth offering "Resume from …".
func IsResumable(positionMs, durationMs int64) bool {
	if positionMs < MinResumeMs {
		return false
	}
	if durationMs > 0 && float64(positionMs) < MinResumeFrac*float64(durationMs) {
		return false
	}
	return true
}

// MarkWatched is the explicit "mark watched" action.
func MarkWatched(prev State, now time.Time) State {
	next := prev
	if !prev.Watched {
		next.PlayCount++
	}
	next.Watched = true
	next.PositionMs = 0
	next.LastPlayedAt = now
	return next
}

// MarkUnwatched is the explicit "mark unwatched" action.
func MarkUnwatched(prev State) State {
	next := prev
	next.Watched = false
	next.PositionMs = 0
	return next
}
