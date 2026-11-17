// Package library scans media folders into the store (PLAN.md sections 3.2–3.5).
// Library folders are only ever read.
package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/acip/minimedia/server/internal/config"
	"github.com/acip/minimedia/server/internal/fsutil"
	"github.com/acip/minimedia/server/internal/naming"
	"github.com/acip/minimedia/server/internal/probe"
	"github.com/acip/minimedia/server/internal/store"
	"github.com/acip/minimedia/server/internal/subs"
	"github.com/acip/minimedia/server/internal/textutil"
)

// Unmatched is a video file that couldn't be parsed.
type Unmatched struct {
	Library string `json:"library"`
	Path    string `json:"path"`
	Reason  string `json:"reason"`
	rel     string
}

type LibraryStatus struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Items int    `json:"items"`
	Error string `json:"error,omitempty"`
}

type Status struct {
	Scanning    bool            `json:"scanning"`
	StartedAt   *time.Time      `json:"startedAt"`
	FinishedAt  *time.Time      `json:"finishedAt"`
	FilesSeen   int             `json:"filesSeen"`
	Probed      int             `json:"probed"`
	ProbeErrors int             `json:"probeErrors"`
	Unmatched   int             `json:"unmatched"`
	Libraries   []LibraryStatus `json:"libraries"`
	// Folder-watch rescans of single folders (not counted above).
	LastChangeScanAt *time.Time `json:"lastChangeScanAt"`
	PendingChanges   bool       `json:"pendingChanges"`
}

type Scanner struct {
	store   *store.Store
	libs    []config.Library
	ffprobe string
	log     *slog.Logger
	workers int

	// OnChange, if set, is called after every scan (full or partial) with
	// whether anything was added, changed or removed.
	OnChange func(changed bool)

	trigger chan struct{}
	changed chan struct{}

	mu          sync.Mutex
	status      Status
	unmatched   map[string][]Unmatched // per library
	pending     map[string]*scope      // folder-watch changes waiting for the debounce
	firstChange time.Time
	lastChange  time.Time
}

func NewScanner(st *store.Store, libs []config.Library, ffprobe string, log *slog.Logger) *Scanner {
	return &Scanner{
		store: st, libs: libs, ffprobe: ffprobe, log: log, workers: 4,
		trigger:   make(chan struct{}, 1),
		changed:   make(chan struct{}, 1),
		unmatched: map[string][]Unmatched{},
	}
}

// Libraries returns the configured libraries.
func (s *Scanner) Libraries() []config.Library { return s.libs }

// Trigger requests a full scan; requests made while one is running coalesce
// into a single follow-up scan.
func (s *Scanner) Trigger() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

// Run scans at startup, then on Trigger, on folder changes (debounced) and
// every `every` (0 = no periodic scan), until ctx is cancelled. Scans never
// overlap.
func (s *Scanner) Run(ctx context.Context, every time.Duration) {
	var tick <-chan time.Time
	if every > 0 {
		t := time.NewTicker(every)
		defer t.Stop()
		tick = t.C
	}
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	defer debounce.Stop()
	armed := false
	arm := func() {
		wait, ok := s.pendingDue(time.Now())
		if !ok {
			return
		}
		debounce.Reset(max(wait, 0))
		armed = true
	}

	s.Trigger()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.trigger:
			s.runFull(ctx)
		case <-tick:
			s.runFull(ctx)
		case <-s.changed:
			arm()
			continue
		case <-debounce.C:
			armed = false
			if wait, ok := s.pendingDue(time.Now()); ok && wait > 0 {
				arm() // more changes arrived since the timer was set
				continue
			}
			s.runPending(ctx)
		}
		if !armed {
			arm() // changes noted while a scan ran
		}
	}
}

func (s *Scanner) runFull(ctx context.Context) {
	changed, err := s.ScanAll(ctx)
	if err != nil && ctx.Err() == nil {
		s.log.Error("scan finished with errors", "err", err)
	}
	if s.OnChange != nil && ctx.Err() == nil {
		// Always after a full scan: metadata may be pending from earlier runs
		// (e.g. a TMDB key added since) even if no file changed.
		s.OnChange(true)
	}
	_ = changed
}

func (s *Scanner) runPending(ctx context.Context) {
	changed := false
	for _, lib := range s.libs {
		sc := s.takePendingFor(lib.Name)
		if sc == nil {
			continue
		}
		res, err := s.scanLibrary(ctx, lib, sc)
		changed = changed || res.changed
		if err != nil {
			if ctx.Err() == nil {
				s.log.Warn("folder change rescan failed", "library", lib.Name, "err", err)
			}
			continue
		}
		s.setUnmatched(lib.Name, sc, res.unmatched)
		s.log.Debug("folder change rescanned", "library", lib.Name, "items", res.items, "changed", res.changed)
	}
	if err := s.store.PurgeOrphans(ctx, time.Now()); err != nil && ctx.Err() == nil {
		s.log.Warn("purge failed", "err", err)
	}
	now := time.Now()
	s.mu.Lock()
	s.status.LastChangeScanAt = &now
	s.mu.Unlock()
	if s.OnChange != nil && ctx.Err() == nil {
		s.OnChange(changed)
	}
}

func (s *Scanner) takePendingFor(library string) *scope {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := s.pending[library]
	delete(s.pending, library)
	return sc
}

func (s *Scanner) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	st.Libraries = append([]LibraryStatus(nil), s.status.Libraries...)
	st.PendingChanges = len(s.pending) > 0
	return st
}

func (s *Scanner) Unmatched() []Unmatched {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Unmatched{}
	for _, lib := range s.libs {
		out = append(out, s.unmatched[lib.Name]...)
	}
	return out
}

// setUnmatched replaces a library's unmatched files within a scope.
func (s *Scanner) setUnmatched(library string, sc *scope, list []Unmatched) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keep []Unmatched
	if !sc.all {
		for _, u := range s.unmatched[library] {
			if !sc.includesRel(u.rel) {
				keep = append(keep, u)
			}
		}
	}
	keep = append(keep, list...)
	sort.Slice(keep, func(i, j int) bool { return keep[i].Path < keep[j].Path })
	s.unmatched[library] = keep
	total := 0
	for _, l := range s.unmatched {
		total += len(l)
	}
	s.status.Unmatched = total
}

// ScanAll does a full scan of every library. changed reports whether any item
// was added, re-probed or removed.
func (s *Scanner) ScanAll(ctx context.Context) (changed bool, err error) {
	start := time.Now()
	s.mu.Lock()
	s.status = Status{Scanning: true, StartedAt: &start, LastChangeScanAt: s.status.LastChangeScanAt, Unmatched: s.status.Unmatched}
	s.mu.Unlock()
	s.log.Info("library scan started")

	var libStatus []LibraryStatus
	var errs []error
	for _, lib := range s.libs {
		if ctx.Err() != nil {
			break
		}
		res, err := s.scanLibrary(ctx, lib, fullScope())
		changed = changed || res.changed
		ls := LibraryStatus{Name: lib.Name, Type: lib.Type, Items: res.items}
		if err != nil {
			ls.Error = err.Error()
			errs = append(errs, fmt.Errorf("%s: %w", lib.Name, err))
			s.log.Error("library scan failed", "library", lib.Name, "err", err)
		} else {
			s.setUnmatched(lib.Name, fullScope(), res.unmatched)
		}
		libStatus = append(libStatus, ls)
	}
	if err := s.store.PurgeOrphans(ctx, time.Now()); err != nil {
		errs = append(errs, err)
	}

	end := time.Now()
	s.mu.Lock()
	s.status.Scanning = false
	s.status.FinishedAt = &end
	s.status.Libraries = libStatus
	st := s.status
	s.mu.Unlock()
	s.log.Info("library scan finished", "took", end.Sub(start).Round(time.Millisecond), "files", st.FilesSeen,
		"probed", st.Probed, "probeErrors", st.ProbeErrors, "unmatched", st.Unmatched)
	return changed, errors.Join(errs...)
}

type videoFile struct {
	path string
	name string
	dir  string
	info fs.FileInfo
}

// work is one item to write, possibly after probing.
type work struct {
	item      *store.Item
	needProbe bool
	external  []store.Subtitle
	images    []store.Image
}

type scanResult struct {
	items     int
	unmatched []Unmatched
	changed   bool
}

// scanLibrary scans the part of a library selected by sc.
func (s *Scanner) scanLibrary(ctx context.Context, lib config.Library, sc *scope) (scanResult, error) {
	var res scanResult
	root := lib.Path
	if _, err := os.ReadDir(root); err != nil {
		// Never treat an unreachable library (NAS asleep, drive unplugged) as empty.
		return res, fmt.Errorf("library folder not readable, keeping existing items: %w", err)
	}
	existing, err := s.store.LibraryFiles(ctx, lib.Name)
	if err != nil {
		return res, err
	}

	var videos []videoFile
	subsByDir := map[string][]string{}      // folder -> subtitle file names
	imagesByDir := map[string][]imageFile{} // folder -> artwork files
	hintByDir := map[string]int64{}         // folder -> id from tmdb.txt
	childDirs := map[string][]string{}      // folder -> subfolder names (for Subs/ lookups)
	var errDirs []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			s.log.Warn("cannot read", "path", path, "err", err)
			errDirs = append(errDirs, path)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := d.Name()
		topLevel := path != root && filepath.Dir(path) == root
		if d.IsDir() {
			if path == root {
				return nil
			}
			if naming.IgnoredDir(name) || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if topLevel && !sc.includesTopDir(name) {
				return filepath.SkipDir
			}
			if fi, err := d.Info(); err == nil && fsutil.IsHidden(fi) {
				return filepath.SkipDir
			}
			parent := filepath.Dir(path)
			childDirs[parent] = append(childDirs[parent], name)
			return nil
		}
		if topLevel && !sc.all && !sc.rootFiles {
			return nil
		}
		if strings.HasPrefix(name, ".") || !d.Type().IsRegular() {
			return nil
		}
		dir := filepath.Dir(path)
		switch {
		case naming.IsVideo(name):
			fi, err := d.Info()
			if err != nil {
				errDirs = append(errDirs, path)
				return nil
			}
			if fsutil.IsHidden(fi) || naming.IsSample(name, fi.Size()) {
				return nil
			}
			videos = append(videos, videoFile{path: path, name: name, dir: dir, info: fi})
		case subs.IsSubtitle(name):
			subsByDir[dir] = append(subsByDir[dir], name)
		case isImage(name):
			if fi, err := d.Info(); err == nil {
				imagesByDir[dir] = append(imagesByDir[dir], imageFileOf(name, fi))
			}
		case strings.EqualFold(name, "tmdb.txt"):
			if id := readHint(path); id > 0 {
				hintByDir[dir] = id
			}
		}
		return nil
	})
	if walkErr != nil {
		return res, walkErr
	}

	if sc.all {
		s.mu.Lock()
		s.status.FilesSeen += len(videos)
		s.mu.Unlock()
	}

	videosPerDir := map[string]int{}
	stemsPerDir := map[string][]string{}
	for _, v := range videos {
		videosPerDir[v.dir]++
		stemsPerDir[v.dir] = append(stemsPerDir[v.dir], stem(v.name))
	}

	shows := map[string]*store.Show{}
	var works []*work
	seen := map[string]bool{}
	var busy scope

	for _, v := range videos {
		id := ItemID(v.path)
		if seen[id] {
			continue // same file reached twice (case variants on a case-insensitive FS)
		}
		rel, _ := filepath.Rel(root, v.path)
		it := &store.Item{
			ID: id, Library: lib.Name, Path: v.path, RelPath: rel,
			Size: v.info.Size(), MtimeMs: v.info.ModTime().UnixMilli(),
		}
		var images []store.Image

		if le, isEp := naming.ParseLooseEpisode(v.name); lib.Type == "movies" && isEp {
			// TV mixed into a movies library: group by show name (PLAN.md 3.2).
			ls, ok := looseShow(root, rel, le)
			if !ok {
				res.unmatched = append(res.unmatched, Unmatched{lib.Name, v.path, "episode code without a show name in the file or folder name", rel})
				continue
			}
			sh := shows[ls.id]
			if sh == nil {
				sh = &store.Show{ID: ls.id, Library: lib.Name, Path: root, Title: ls.title,
					SortTitle: textutil.SortKey(ls.title), Year: ls.year}
				shows[ls.id] = sh
			}
			if sh.Year == 0 {
				sh.Year = ls.year
			}
			// Artwork and tmdb.txt come from the show's own folder, if it has one.
			if ls.folder != "" && (sh.Path == root || ls.folder < sh.Path) {
				sh.Path, sh.TMDBHint = ls.folder, hintByDir[ls.folder]
			}
			it.Kind, it.ShowID, it.Season, it.Episode, it.EpisodeEnd = "episode", sh.ID, le.Season, le.Episode.Episode, le.EpisodeEnd
			it.Title = le.Title
			it.SortTitle = textutil.SortKey(le.Title)
			images = episodeArt(v.dir, stem(v.name), imagesByDir[v.dir])
		} else if lib.Type == "movies" {
			m := naming.ParseMovie(v.name)
			ownFolder := v.dir != root && videosPerDir[v.dir] == 1
			if ownFolder {
				if fm, ok := naming.ParseFolder(filepath.Base(v.dir)); ok {
					m = fm
				}
				it.TMDBHint = hintByDir[v.dir]
			}
			it.Kind, it.Title, it.Year = "movie", m.Title, m.Year
			it.SortTitle = textutil.SortKey(m.Title)
			images = movieArt(v.dir, stem(v.name), imagesByDir[v.dir], ownFolder)
		} else {
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) < 2 {
				res.unmatched = append(res.unmatched, Unmatched{lib.Name, v.path, "file is not inside a show folder", rel})
				continue
			}
			ep, ok := naming.ParseEpisode(v.name)
			if !ok {
				res.unmatched = append(res.unmatched, Unmatched{lib.Name, v.path, "no episode number (S01E02, 1x02 or E02) in the file name", rel})
				continue
			}
			if !ep.HasSeason {
				season, found := -1, false
				for i := len(parts) - 2; i >= 1; i-- {
					if n, ok := naming.ParseSeasonFolder(parts[i]); ok {
						season, found = n, true
						break
					}
				}
				if !found {
					res.unmatched = append(res.unmatched, Unmatched{lib.Name, v.path, "episode number without a season, and not inside a Season folder", rel})
					continue
				}
				ep.Season = season
			}
			showDir := filepath.Join(root, parts[0])
			sh := shows[ShowID(showDir)]
			if sh == nil {
				pm := naming.ParseShowFolder(parts[0])
				sh = &store.Show{ID: ShowID(showDir), Library: lib.Name, Path: showDir, Title: pm.Title,
					SortTitle: textutil.SortKey(pm.Title), Year: pm.Year, TMDBHint: hintByDir[showDir]}
				shows[sh.ID] = sh
			}
			it.Kind, it.ShowID, it.Season, it.Episode, it.EpisodeEnd = "episode", sh.ID, ep.Season, ep.Episode, ep.EpisodeEnd
			it.Title = ep.Title
			it.SortTitle = textutil.SortKey(ep.Title)
			images = episodeArt(v.dir, stem(v.name), imagesByDir[v.dir])
		}
		seen[id] = true

		prev, exists := existing[id]
		w := &work{item: it, images: images}
		w.needProbe = !exists || !prev.Probed || prev.Size != it.Size || prev.MtimeMs != it.MtimeMs
		if w.needProbe && fsutil.InUse(v.path) {
			// Still being copied or downloaded: leave any existing row alone and
			// look again shortly (the folder watcher may not report the end).
			s.log.Info("file is still being written; will retry", "path", v.path)
			busy.merge(scopeOf(rel))
			continue
		}
		if exists {
			it.AddedAt = prev.AddedAt
		} else {
			it.AddedAt = fsutil.AddedTime(v.info).UnixMilli()
		}
		res.changed = res.changed || w.needProbe
		w.external = externalSubs(id, v, subsByDir, childDirs, stemsPerDir[v.dir])
		works = append(works, w)
	}
	if busy.all || busy.rootFiles || len(busy.dirs) > 0 {
		b := busy
		time.AfterFunc(busyRetry, func() { s.addPending(lib.Name, &b) })
	}

	// Shows first, then items (probing in parallel, writing in batches).
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return res, err
	}
	for _, sh := range shows {
		sh.AddedAt = time.Now().UnixMilli()
		err := tx.UpsertShow(sh)
		if err == nil && sh.Path != root {
			// A show whose episodes all sit loose in the library root has no
			// folder of its own; the root's pictures aren't its artwork.
			err = tx.ReplaceImages(sh.ID, "local", showArt(sh.Path, imagesByDir[sh.Path]))
		}
		if err != nil {
			tx.Rollback()
			return res, err
		}
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	res.items = len(works)
	if err := s.probeAndWrite(ctx, works); err != nil {
		return res, err
	}

	// Remove items whose files are gone, except under folders we failed to
	// read, files still being written, and anything outside the scope.
	var gone []string
	for id, f := range existing {
		if seen[id] || !sc.includesRel(f.RelPath) {
			continue
		}
		if underAny(filepath.Join(root, f.RelPath), errDirs) {
			continue
		}
		gone = append(gone, id)
	}
	if len(gone) > 0 {
		tx, err := s.store.Begin(ctx)
		if err != nil {
			return res, err
		}
		if err := tx.DeleteItems(gone, time.Now()); err != nil {
			tx.Rollback()
			return res, err
		}
		if err := tx.Commit(); err != nil {
			return res, err
		}
		res.changed = true
		s.log.Info("removed missing files", "library", lib.Name, "count", len(gone))
	}
	for _, u := range res.unmatched {
		s.log.Warn("unmatched file skipped", "library", u.Library, "path", u.Path, "reason", u.Reason)
	}
	return res, nil
}

type looseShowInfo struct {
	id, title string
	year      int
	folder    string // the show's own folder, "" when its episodes sit loose
}

// looseShow names the show of an episode found in a movies library. The
// nearest folder that isn't a "Season N" folder names the show when it agrees
// with the file name ("Show (2025)/Season 2/Show.S02E01.mkv", or a season
// pack "Show.S02.1080p/Show.S02E01.mkv") or when the file name has no show
// name; otherwise the file name does. Episodes of one show are grouped by
// name across the library, wherever they sit.
func looseShow(root, rel string, le naming.LooseEpisode) (looseShowInfo, bool) {
	parts := strings.Split(rel, string(filepath.Separator))
	var folder naming.Movie
	folderPath := ""
	for i := len(parts) - 2; i >= 0; i-- {
		name := parts[i]
		if _, ok := naming.ParseSeasonFolder(name); ok {
			continue
		}
		if pm, _, ok := naming.ParseSeasonPack(name); ok {
			folder = pm
		} else {
			folder = naming.ParseShowFolder(name)
		}
		folderPath = filepath.Join(append([]string{root}, parts[:i+1]...)...)
		break
	}
	key := func(t string) string { return strings.Join(textutil.Words(t), " ") }
	var out looseShowInfo
	switch {
	case folder.Title != "" && (le.Show.Title == "" || key(folder.Title) == key(le.Show.Title)):
		out = looseShowInfo{title: folder.Title, year: folder.Year, folder: folderPath}
		if out.year == 0 {
			out.year = le.Show.Year
		}
	case le.Show.Title != "":
		out = looseShowInfo{title: le.Show.Title, year: le.Show.Year}
	default:
		return looseShowInfo{}, false
	}
	k := key(out.title)
	if k == "" {
		return looseShowInfo{}, false
	}
	out.id = hash16("show:" + NormPath(root) + "|" + k)
	return out, true
}

// scopeOf is the scope covering one item path relative to the root.
func scopeOf(rel string) *scope {
	first, _, nested := strings.Cut(filepath.ToSlash(rel), "/")
	if !nested {
		return &scope{rootFiles: true}
	}
	sc := &scope{}
	sc.addDir(first)
	return sc
}

func (s *Scanner) probeAndWrite(ctx context.Context, works []*work) error {
	type result struct {
		w     *work
		info  *probe.Info
		probe string
	}
	jobs := make(chan *work)
	results := make(chan result)
	var wg sync.WaitGroup
	for i := 0; i < s.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for w := range jobs {
				r := result{w: w}
				if w.needProbe {
					raw, err := probe.Run(ctx, s.ffprobe, fsutil.LongPath(w.item.Path))
					if err == nil {
						r.info, err = probe.Parse(raw)
					}
					if err != nil {
						if ctx.Err() == nil {
							s.log.Warn("probe failed; will retry on the next scan", "path", w.item.Path, "err", err)
						}
						s.mu.Lock()
						s.status.ProbeErrors++
						s.mu.Unlock()
					} else {
						r.probe = string(raw)
						s.mu.Lock()
						s.status.Probed++
						s.mu.Unlock()
					}
				}
				select {
				case results <- r:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, w := range works {
			select {
			case jobs <- w:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	const batch = 25
	var tx *store.Tx
	n := 0
	var firstErr error
	for r := range results {
		if firstErr != nil {
			continue // drain
		}
		if tx == nil {
			var err error
			if tx, err = s.store.Begin(ctx); err != nil {
				firstErr = err
				continue
			}
		}
		if err := writeOne(tx, r.w, r.info, r.probe); err != nil {
			firstErr = err
			tx.Rollback()
			tx = nil
			continue
		}
		if n++; n%batch == 0 {
			if err := tx.Commit(); err != nil {
				firstErr = err
			}
			tx = nil
		}
	}
	if tx != nil {
		if firstErr == nil {
			firstErr = tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	return firstErr
}

func writeOne(tx *store.Tx, w *work, info *probe.Info, raw string) error {
	it := w.item
	if info != nil {
		it.ProbeJSON = raw
		it.RuntimeMs = info.DurationMs
	}
	if err := tx.UpsertItem(it); err != nil {
		return err
	}
	if info != nil {
		var emb []store.Subtitle
		for _, s := range info.Subtitles {
			emb = append(emb, store.Subtitle{
				ID: SubtitleID(it.ID, "emb:"+strconv.Itoa(s.Index)), Source: "embedded", StreamIndex: s.Index,
				Language: s.Language, Label: s.Title, Forced: s.Forced, SDH: s.SDH, Codec: s.Codec,
			})
		}
		if err := tx.ReplaceSubtitles(it.ID, "embedded", emb); err != nil {
			return err
		}
		var ms []store.Marker
		for _, m := range info.Markers {
			ms = append(ms, store.Marker{Kind: m.Kind, StartMs: m.StartMs, EndMs: m.EndMs})
		}
		if err := tx.ReplaceMarkers(it.ID, ms); err != nil {
			return err
		}
	}
	if err := tx.ReplaceImages(it.ID, "local", w.images); err != nil {
		return err
	}
	return tx.ReplaceSubtitles(it.ID, "external", w.external)
}

// externalSubs finds sidecars for a video in its folder, in Subs/ or
// Subtitles/ next to it, and in Subs/<video name>/ (PLAN.md 3.4).
func externalSubs(itemID string, v videoFile, subsByDir, childDirs map[string][]string, dirStems []string) []store.Subtitle {
	videoStem := stem(v.name)
	var out []store.Subtitle
	add := func(dir, name string, info subs.Info) {
		p := filepath.Join(dir, name)
		out = append(out, store.Subtitle{
			ID: SubtitleID(itemID, "ext:"+NormPath(p)), Source: "external", Path: p,
			Language: info.Language, Label: info.Label, Forced: info.Forced, SDH: info.SDH, Codec: info.Codec,
		})
	}
	// Folders holding "X.<lang>.srt"-style names.
	dirs := []string{v.dir}
	for _, c := range childDirs[v.dir] {
		if subs.IsSubFolder(c) {
			dirs = append(dirs, filepath.Join(v.dir, c))
		}
	}
	for _, dir := range dirs {
		for _, name := range subsByDir[dir] {
			info, ok := subs.MatchName(videoStem, name)
			if !ok || longerStemMatches(name, videoStem, dirStems) {
				continue
			}
			add(dir, name, info)
		}
		// Subs/<video stem>/*.srt
		if dir == v.dir {
			continue
		}
		for _, c := range childDirs[dir] {
			if !strings.EqualFold(c, videoStem) {
				continue
			}
			inner := filepath.Join(dir, c)
			for _, name := range subsByDir[inner] {
				if info, ok := subs.ParseLoose(name); ok {
					add(inner, name, info)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// longerStemMatches reports whether another video in the same folder has a
// longer stem that also matches the subtitle ("Movie.Part2.en.srt" belongs to
// "Movie.Part2.mkv", not "Movie.mkv").
func longerStemMatches(subName, stem string, stems []string) bool {
	for _, other := range stems {
		if len(other) > len(stem) {
			if _, ok := subs.MatchName(other, subName); ok {
				return true
			}
		}
	}
	return false
}

func stem(name string) string { return strings.TrimSuffix(name, filepath.Ext(name)) }

func underAny(p string, dirs []string) bool {
	np := NormPath(p)
	for _, d := range dirs {
		nd := NormPath(d)
		if np == nd || strings.HasPrefix(np, nd+"/") {
			return true
		}
	}
	return false
}
