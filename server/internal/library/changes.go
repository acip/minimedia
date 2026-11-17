package library

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/acip/minimedia/server/internal/naming"
	"github.com/acip/minimedia/server/internal/subs"
)

// Folder-watch debouncing (PLAN.md 3.5): a change is rescanned once the
// library has been quiet for debounceQuiet, but no later than debounceMax
// after the first change (so a long copy session still shows progress).
var (
	debounceQuiet = 5 * time.Second
	debounceMax   = 30 * time.Second
	busyRetry     = 30 * time.Second // files still being written are retried after this
)

// scope limits a scan to some top-level entries of a library root.
type scope struct {
	all       bool
	dirs      map[string]bool // NormName of top-level folders
	rootFiles bool            // files directly in the root, and root-level Subs folders
}

func fullScope() *scope { return &scope{all: true} }

func (sc *scope) merge(o *scope) {
	if o.all {
		sc.all = true
	}
	sc.rootFiles = sc.rootFiles || o.rootFiles
	for d := range o.dirs {
		if sc.dirs == nil {
			sc.dirs = map[string]bool{}
		}
		sc.dirs[d] = true
	}
}

func (sc *scope) addDir(name string) {
	if sc.dirs == nil {
		sc.dirs = map[string]bool{}
	}
	sc.dirs[normName(name)] = true
}

// includesTopDir reports whether a folder directly under the root is scanned.
func (sc *scope) includesTopDir(name string) bool {
	return sc.all || sc.dirs[normName(name)] || (sc.rootFiles && subs.IsSubFolder(name))
}

// includesRel reports whether an item at rel (relative to the root) is covered.
func (sc *scope) includesRel(rel string) bool {
	if sc.all {
		return true
	}
	first, _, nested := strings.Cut(filepath.ToSlash(rel), "/")
	if !nested {
		return sc.rootFiles
	}
	return sc.includesTopDir(first)
}

func normName(name string) string { return NormPath(name) }

// noiseExts are files whose changes never affect the library.
var noiseExts = map[string]bool{
	".part": true, ".partial": true, ".!qb": true, ".!ut": true, ".crdownload": true, ".download": true,
	".tmp": true, ".temp": true, ".nfo": true, ".db": true, ".ini": true, ".lnk": true, ".url": true,
	".torrent": true, ".log": true, ".ds_store": true,
}

// changeScope maps a changed path (relative to the library root) to the part
// of the library to rescan. ok is false for changes that can be ignored.
func changeScope(rel string) (sc *scope, ok bool) {
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" {
		return fullScope(), true
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, p := range parts {
		if p == ".." {
			return fullScope(), true
		}
		if naming.IgnoredDir(p) || strings.HasPrefix(p, ".") {
			return nil, false
		}
	}
	last := parts[len(parts)-1]
	if noiseExts[strings.ToLower(filepath.Ext(last))] {
		return nil, false
	}
	sc = &scope{}
	sc.addDir(parts[0])
	// A top-level entry may be a file in the root (or a deleted one we can't
	// stat any more): cover the root files too. That is cheap: unchanged files
	// are never re-probed.
	if len(parts) == 1 || subs.IsSubFolder(parts[0]) {
		sc.rootFiles = true
	}
	return sc, true
}

// Changed records a change reported by the folder watcher. rel is relative to
// the library root; "" means "rescan the whole library" (e.g. a lost-events
// overflow). The rescan runs after the debounce delay.
func (s *Scanner) Changed(library, rel string) {
	sc, ok := changeScope(rel)
	if !ok {
		return
	}
	s.addPending(library, sc)
}

func (s *Scanner) addPending(library string, sc *scope) {
	now := time.Now()
	s.mu.Lock()
	if s.pending == nil {
		s.pending = map[string]*scope{}
	}
	if p := s.pending[library]; p != nil {
		p.merge(sc)
	} else {
		cp := &scope{}
		cp.merge(sc)
		s.pending[library] = cp
		if len(s.pending) == 1 {
			s.firstChange = now
		}
	}
	s.lastChange = now
	s.mu.Unlock()
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// pendingDue returns how long until pending changes should be scanned (≤ 0 =
// now), and whether anything is pending.
func (s *Scanner) pendingDue(now time.Time) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return 0, false
	}
	due := s.lastChange.Add(debounceQuiet)
	if max := s.firstChange.Add(debounceMax); max.Before(due) {
		due = max
	}
	return due.Sub(now), true
}

func (s *Scanner) takePending() map[string]*scope {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pending
	s.pending = nil
	return p
}
