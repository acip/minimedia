package watch

import (
	"context"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/acip/minimedia/server/internal/naming"
)

// pollEvery is how often pollTree looks for changes.
var pollEvery = 30 * time.Second

type stamp struct {
	size int64
	mod  int64
	dir  bool
}

// snapshot records the size and modification time of everything under root
// that the library could use (ignored and hidden folders are skipped).
func snapshot(root string) (map[string]stamp, error) {
	m := map[string]stamp{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // unreadable subfolder: skip it
		}
		if p == root {
			return nil
		}
		if naming.IgnoredDir(d.Name()) { // also covers dot-files
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		m[rel] = stamp{size: info.Size(), mod: info.ModTime().UnixNano(), dir: d.IsDir()}
		return nil
	})
	return m, err
}

// pollTree finds changes by walking the tree every pollEvery and comparing
// sizes and modification times with the previous walk. It is used where
// change notifications don't fit: on macOS kqueue needs an open file per
// watched file, which a large library runs out of, and network mounts send
// no notifications at all. A walk only reads folder listings, so it is cheap
// even for thousands of files.
func pollTree(ctx context.Context, root string, fn Func, ready func()) error {
	prev, err := snapshot(root)
	if err != nil {
		return err
	}
	ready()
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		cur, err := snapshot(root)
		if err != nil {
			return err // the folder or its share is gone: Run retries
		}
		for rel, s := range cur {
			// A folder's own time changes when entries are added or removed;
			// those entries are reported themselves.
			if p, ok := prev[rel]; !ok || (!s.dir && p != s) {
				fn(rel)
			}
		}
		for rel := range prev {
			if _, ok := cur[rel]; !ok {
				fn(rel)
			}
		}
		prev = cur
	}
}
