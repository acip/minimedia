// Package watch reports changes under a folder tree (PLAN.md 3.5).
//
// On Windows it uses one ReadDirectoryChangesW subtree watch per library
// root. fsnotify can only watch single folders there, and keeping a handle
// open on every folder would stop the user from renaming or deleting those
// folders while the server runs. On Linux it uses fsnotify (inotify) with a
// watch per folder. On macOS it polls (see pollTree).
package watch

import (
	"context"
	"log/slog"
	"time"
)

// Func receives a changed path relative to the watched root. An empty path
// means events were lost and the whole tree should be rescanned.
type Func func(rel string)

// Run watches root until ctx is cancelled. When watching fails (the folder
// disappears, a network share drops), it logs, waits and starts again, asking
// for a full rescan each time it recovers.
func Run(ctx context.Context, root string, log *slog.Logger, fn Func) {
	const retry = time.Minute
	failed := false
	for ctx.Err() == nil {
		err := watchTree(ctx, root, fn, func() {
			if failed {
				log.Info("folder watching resumed", "path", root)
				fn("") // whatever happened while we weren't watching
				failed = false
			}
		})
		if ctx.Err() != nil {
			return
		}
		if !failed {
			log.Warn("folder watching stopped; periodic rescans still run. Retrying every minute", "path", root, "err", err)
			failed = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
}
