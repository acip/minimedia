package library

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/acip/minimedia/server/internal/config"
	"github.com/acip/minimedia/server/internal/store"
)

func TestChangeScope(t *testing.T) {
	cases := []struct {
		rel       string
		ok        bool
		all       bool
		dir       string
		rootFiles bool
	}{
		{"", true, true, "", false},
		{"Movie (2020)", true, false, "Movie (2020)", true},
		{filepath.Join("Movie (2020)", "Subs", "x.srt"), true, false, "Movie (2020)", false},
		{"Film.2019.mkv", true, false, "Film.2019.mkv", true},
		{filepath.Join("Subs", "Film.2019.ro.srt"), true, false, "Subs", true},
		{filepath.Join("Movie (2020)", "Movie.mkv.part"), false, false, "", false},
		{filepath.Join("Movie (2020)", "desktop.ini"), false, false, "", false},
		{filepath.Join("$RECYCLE.BIN", "x.mkv"), false, false, "", false},
		{filepath.Join("Show", ".trickplay", "1.jpg"), false, false, "", false},
	}
	for _, c := range cases {
		sc, ok := changeScope(c.rel)
		if ok != c.ok {
			t.Errorf("%q: ok = %v", c.rel, ok)
			continue
		}
		if !ok {
			continue
		}
		dir := ""
		for d := range sc.dirs {
			dir = d
		}
		if c.dir != "" {
			c.dir = normName(c.dir)
		}
		if sc.all != c.all || dir != c.dir || sc.rootFiles != c.rootFiles {
			t.Errorf("%q: got all=%v dir=%q root=%v", c.rel, sc.all, dir, sc.rootFiles)
		}
	}
	sc := &scope{}
	sc.addDir("Movie (2020)")
	if !sc.includesRel(filepath.Join("Movie (2020)", "a.mkv")) || sc.includesRel("b.mkv") || sc.includesRel(filepath.Join("Other", "c.mkv")) {
		t.Error("includesRel wrong")
	}
}

func TestLocalArtworkAndHints(t *testing.T) {
	root := t.TempDir()
	movies := filepath.Join(root, "Movies")
	shows := filepath.Join(root, "Shows")
	touch(t, movies,
		"Own (2010)/Own (2010).mkv", "Own (2010)/poster.jpg", "Own (2010)/folder.jpg", "Own (2010)/fanart.png",
		"Own (2010)/clearlogo.png", "Own (2010)/tmdb.txt",
		"Shared.2011.mkv", "Shared.2011-poster.jpg", "Other.2012.mkv", "poster.jpg", // a root poster.jpg belongs to nobody
	)
	touch(t, shows,
		"Show (2020)/poster.jpg", "Show (2020)/backdrop.jpg", "Show (2020)/tmdb.txt",
		"Show (2020)/Season 01/Show - S01E01.mkv", "Show (2020)/Season 01/Show - S01E01-thumb.jpg",
		"Show (2020)/Season 01/Show - S01E02.mkv",
	)
	if err := os.WriteFile(filepath.Join(movies, "Own (2010)", "tmdb.txt"), []byte("https://www.themoviedb.org/movie/603-the-matrix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shows, "Show (2020)", "tmdb.txt"), []byte("1399"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := openStore(t)
	sc := NewScanner(st, []config.Library{{Name: "M", Type: "movies", Path: movies}, {Name: "S", Type: "shows", Path: shows}}, "x", quietLog())
	ctx := context.Background()
	if _, err := sc.ScanAll(ctx); err != nil {
		t.Fatal(err)
	}
	art := func(owner, kind string) string {
		t.Helper()
		im, err := st.BestImage(ctx, owner, kind)
		if err != nil {
			return ""
		}
		r, _ := filepath.Rel(root, im.Path)
		return filepath.ToSlash(r)
	}
	own := ItemID(filepath.Join(movies, "Own (2010)", "Own (2010).mkv"))
	for kind, want := range map[string]string{"poster": "Movies/Own (2010)/poster.jpg", "backdrop": "Movies/Own (2010)/fanart.png",
		"logo": "Movies/Own (2010)/clearlogo.png"} {
		if got := art(own, kind); got != want {
			t.Errorf("own-folder %s = %q, want %q", kind, got, want)
		}
	}
	shared := ItemID(filepath.Join(movies, "Shared.2011.mkv"))
	if got := art(shared, "poster"); got != "Movies/Shared.2011-poster.jpg" {
		t.Errorf("shared poster = %q", got)
	}
	if got := art(ItemID(filepath.Join(movies, "Other.2012.mkv")), "poster"); got != "" {
		t.Errorf("root poster.jpg must not attach to a root-level movie: %q", got)
	}
	showID := ShowID(filepath.Join(shows, "Show (2020)"))
	if art(showID, "poster") != "Shows/Show (2020)/poster.jpg" || art(showID, "backdrop") != "Shows/Show (2020)/backdrop.jpg" {
		t.Errorf("show art = %q %q", art(showID, "poster"), art(showID, "backdrop"))
	}
	e1 := ItemID(filepath.Join(shows, "Show (2020)", "Season 01", "Show - S01E01.mkv"))
	if art(e1, "still") != "Shows/Show (2020)/Season 01/Show - S01E01-thumb.jpg" {
		t.Errorf("still = %q", art(e1, "still"))
	}

	it, _ := st.GetItem(ctx, own)
	if it.TMDBHint != 603 {
		t.Errorf("movie hint = %d", it.TMDBHint)
	}
	sh, _ := st.GetShow(ctx, showID)
	if sh.TMDBHint != 1399 {
		t.Errorf("show hint = %d", sh.TMDBHint)
	}

	// A deleted poster falls back (nothing left here); versions change with the file.
	before, _ := st.BestImage(ctx, own, "backdrop")
	os.Remove(filepath.Join(movies, "Own (2010)", "poster.jpg"))
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(filepath.Join(movies, "Own (2010)", "fanart.png"), []byte("changed"), 0o644)
	if _, err := sc.ScanAll(ctx); err != nil {
		t.Fatal(err)
	}
	if got := art(own, "poster"); got != "Movies/Own (2010)/folder.jpg" {
		t.Errorf("after deleting poster.jpg: %q", got)
	}
	if after, _ := st.BestImage(ctx, own, "backdrop"); after.Version == before.Version {
		t.Error("image version did not change with the file")
	}
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestFolderChangeRescansOnlyThatFolder(t *testing.T) {
	oldQuiet, oldMax := debounceQuiet, debounceMax
	debounceQuiet, debounceMax = 50*time.Millisecond, 200*time.Millisecond
	defer func() { debounceQuiet, debounceMax = oldQuiet, oldMax }()

	root := t.TempDir()
	touch(t, root, "A (2001)/A (2001).mkv", "B (2002)/B (2002).mkv", "Loose.2003.mkv")
	st := openStore(t)
	sc := NewScanner(st, []config.Library{{Name: "M", Type: "movies", Path: root}}, "x", quietLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scans := make(chan bool, 10)
	sc.OnChange = func(changed bool) { scans <- changed }
	go sc.Run(ctx, 0)
	<-scans // initial full scan

	count := func() int { ms, _ := st.ListMovies(ctx, "title", "", 0); return len(ms) }
	if count() != 3 {
		t.Fatalf("initial movies = %d", count())
	}

	// A new movie folder and a subtitle elsewhere; B is deleted behind the
	// scanner's back but not reported, so a scoped rescan must not notice it.
	touch(t, root, "C (2004)/C (2004).mkv", "A (2001)/A (2001).ro.srt")
	os.RemoveAll(filepath.Join(root, "B (2002)"))
	sc.Changed("M", "C (2004)")
	sc.Changed("M", filepath.Join("C (2004)", "C (2004).mkv"))
	sc.Changed("M", filepath.Join("A (2001)", "A (2001).ro.srt"))
	sc.Changed("M", filepath.Join("A (2001)", "A (2001).mkv.part")) // ignored
	if changed := <-scans; !changed {
		t.Error("rescan reported no change")
	}
	if n := count(); n != 4 {
		t.Errorf("after scoped rescan: %d movies, want 4 (C added, B untouched)", n)
	}
	if _, err := st.GetItem(ctx, ItemID(filepath.Join(root, "C (2004)", "C (2004).mkv"))); err != nil {
		t.Errorf("C not added: %v", err)
	}
	if _, err := st.GetItem(ctx, ItemID(filepath.Join(root, "B (2002)", "B (2002).mkv"))); err != nil {
		t.Errorf("B removed by a scan that didn't cover it: %v", err)
	}
	a := ItemID(filepath.Join(root, "A (2001)", "A (2001).mkv"))
	if subs, _ := st.Subtitles(ctx, a); len(subs) != 1 || subs[0].Language != "ro" {
		t.Errorf("new sidecar not attached: %+v", subs)
	}

	// Reporting the deleted folder removes it; root files survive.
	sc.Changed("M", "B (2002)")
	<-scans
	if n := count(); n != 3 {
		t.Errorf("after deleting B: %d movies, want 3", n)
	}
	if _, err := st.GetItem(ctx, ItemID(filepath.Join(root, "Loose.2003.mkv"))); err != nil {
		t.Errorf("root file removed: %v", err)
	}
	if s := sc.Status(); s.LastChangeScanAt == nil || s.PendingChanges {
		t.Errorf("status = %+v", s)
	}
}

func TestBusyFileIsRetried(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("write-sharing detection is Windows-only")
	}
	oldRetry := busyRetry
	busyRetry = 100 * time.Millisecond
	defer func() { busyRetry = oldRetry }()
	oldQuiet := debounceQuiet
	debounceQuiet = 20 * time.Millisecond
	defer func() { debounceQuiet = oldQuiet }()

	root := t.TempDir()
	p := filepath.Join(root, "Copying (2020).mkv")
	f, err := os.Create(p) // held open for writing, like a copy in progress
	if err != nil {
		t.Fatal(err)
	}
	st := openStore(t)
	sc := NewScanner(st, []config.Library{{Name: "M", Type: "movies", Path: root}}, "x", quietLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sc.Run(ctx, 0)
	id := ItemID(p)
	time.Sleep(300 * time.Millisecond)
	if _, err := st.GetItem(ctx, id); err != store.ErrNotFound {
		t.Errorf("file being written was added: %v", err)
	}
	f.Close()
	waitFor(t, "the finished file to be added", func() bool {
		_, err := st.GetItem(ctx, id)
		return err == nil
	})
}
