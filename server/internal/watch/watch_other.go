//go:build !windows

package watch

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/fsnotify/fsnotify"

	"github.com/acip/minimedia/server/internal/naming"
)

func watchTree(ctx context.Context, root string, fn Func, ready func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	addTree := func(dir string) error {
		return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == dir {
					return err
				}
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if p != root && (naming.IgnoredDir(d.Name()) || d.Name()[0] == '.') {
				return filepath.SkipDir
			}
			return w.Add(p)
		})
	}
	if err := addTree(root); err != nil {
		return err
	}
	ready()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return errors.New("watcher closed")
			}
			if ev.Name == root && (ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename)) {
				return errors.New("library folder was removed or renamed")
			}
			if ev.Has(fsnotify.Create) {
				_ = addTree(ev.Name) // a new folder: watch it and everything in it
			}
			if rel, err := filepath.Rel(root, ev.Name); err == nil {
				fn(rel)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return errors.New("watcher closed")
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				fn("")
				continue
			}
			return err
		}
	}
}
