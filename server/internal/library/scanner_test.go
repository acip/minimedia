package library

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/acip/minimedia/server/internal/config"
	"github.com/acip/minimedia/server/internal/progress"
	"github.com/acip/minimedia/server/internal/store"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func touch(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// Layout tests run without ffprobe (probes fail, items are still listed).
func TestScanLayout(t *testing.T) {
	root := t.TempDir()
	movies := filepath.Join(root, "Movies")
	shows := filepath.Join(root, "Shows")
	touch(t, movies,
		"A Movie (2010)/A Movie (2010).mkv",
		"A Movie (2010)/A Movie (2010).ro.srt",
		"A Movie (2010)/Subs/A Movie (2010).en.forced.srt",
		"A Movie (2010)/Subs/A Movie (2010)/2_English.srt",
		"A Movie (2010)/Subs/A Movie (2010)/3_Romanian.srt",
		"A Movie (2010)/Extras/Making Of.mkv",
		"A Movie (2010)/poster.jpg",
		"Scene.Name.2015.1080p.BluRay.x264-GRP.mkv",
		"Scene.Name.2015.1080p.BluRay.x264-GRP.eng.srt",
		"Two/Movie.mkv",
		"Two/Movie.Part2.mkv",
		"Two/Movie.Part2.en.srt",
		"Two/Movie.ro.srt",
		"Folder Named (2001)/Other Name.mkv",
		"Folder Named (2001)/Second.mkv",
		"random-sample.mkv",
		".hidden/Hidden.mkv",
		"$RECYCLE.BIN/x.mkv",
		"Ștefan și Țara (2019).mp4",
	)
	touch(t, shows,
		"Show (2020)/Season 01/Show - S01E01 - A.mkv",
		"Show (2020)/Season 01/Show - S01E02E03 - Two.mkv",
		"Show (2020)/Season 02/E04 - Folder Season.mkv",
		"Show (2020)/Specials/Show - S00E01.mkv",
		"Show (2020)/random.mkv",
		"Show (2020)/Trailers/Show - S01E01 - Trailer.mkv",
		"Loose S01E01.mkv",
		"Other.Show/S01/Other.Show.1x05.mkv",
	)

	st := openStore(t)
	libs := []config.Library{{Name: "Movies", Type: "movies", Path: movies}, {Name: "Shows", Type: "shows", Path: shows}}
	sc := NewScanner(st, libs, filepath.Join(root, "no-ffprobe.exe"), quietLog())
	ctx := context.Background()
	if _, err := sc.ScanAll(ctx); err != nil {
		t.Fatal(err)
	}

	items, _ := st.AllItems(ctx)
	got := map[string]*store.Item{}
	for _, it := range items {
		got[filepath.ToSlash(it.RelPath)] = it
	}
	wantMovies := map[string][2]any{
		"A Movie (2010)/A Movie (2010).mkv":         {"A Movie", 2010},
		"Scene.Name.2015.1080p.BluRay.x264-GRP.mkv": {"Scene Name", 2015},
		"Two/Movie.mkv":                      {"Movie", 0},
		"Two/Movie.Part2.mkv":                {"Movie Part2", 0},
		"Folder Named (2001)/Other Name.mkv": {"Other Name", 0}, // two videos: folder name not used
		"Folder Named (2001)/Second.mkv":     {"Second", 0},
		"Ștefan și Țara (2019).mp4":          {"Ștefan și Țara", 2019},
	}
	for rel, w := range wantMovies {
		it := got[rel]
		if it == nil {
			t.Errorf("missing movie %s", rel)
			continue
		}
		if it.Kind != "movie" || it.Title != w[0] || it.Year != w[1] {
			t.Errorf("%s = %q (%d) kind %s, want %q (%d)", rel, it.Title, it.Year, it.Kind, w[0], w[1])
		}
	}
	wantEps := map[string][4]any{ // season, episode, end, title
		"Show (2020)/Season 01/Show - S01E01 - A.mkv":      {1, 1, 0, "A"},
		"Show (2020)/Season 01/Show - S01E02E03 - Two.mkv": {1, 2, 3, "Two"},
		"Show (2020)/Season 02/E04 - Folder Season.mkv":    {2, 4, 0, "Folder Season"},
		"Show (2020)/Specials/Show - S00E01.mkv":           {0, 1, 0, ""},
		"Other.Show/S01/Other.Show.1x05.mkv":               {1, 5, 0, ""},
	}
	for rel, w := range wantEps {
		it := got[rel]
		if it == nil {
			t.Errorf("missing episode %s", rel)
			continue
		}
		if it.Kind != "episode" || it.Season != w[0] || it.Episode != w[1] || it.EpisodeEnd != w[2] || it.Title != w[3] {
			t.Errorf("%s = S%dE%d-%d %q, want %v", rel, it.Season, it.Episode, it.EpisodeEnd, it.Title, w)
		}
	}
	if len(items) != len(wantMovies)+len(wantEps) {
		var names []string
		for k := range got {
			names = append(names, k)
		}
		sort.Strings(names)
		t.Errorf("got %d items, want %d: %v", len(items), len(wantMovies)+len(wantEps), names)
	}

	// Shows
	showList, _ := st.ListShows(ctx, "title", 0)
	if len(showList) != 2 || showList[0].Title != "Other Show" || showList[1].Title != "Show" || showList[1].Year != 2020 {
		for _, s := range showList {
			t.Logf("show %+v", *s)
		}
		t.Errorf("shows wrong")
	}

	// Unmatched
	um := sc.Unmatched()
	var umPaths []string
	for _, u := range um {
		r, _ := filepath.Rel(shows, u.Path)
		umPaths = append(umPaths, filepath.ToSlash(r))
	}
	sort.Strings(umPaths)
	if strings.Join(umPaths, "|") != "Loose S01E01.mkv|Show (2020)/random.mkv" {
		t.Errorf("unmatched = %v", umPaths)
	}

	// Sidecars
	subsOf := func(rel string) []string {
		subsList, _ := st.Subtitles(ctx, got[rel].ID)
		var out []string
		for _, s := range subsList {
			r, _ := filepath.Rel(filepath.Dir(got[rel].Path), s.Path)
			flag := ""
			if s.Forced {
				flag = "+forced"
			}
			out = append(out, filepath.ToSlash(r)+"="+s.Language+flag)
		}
		return out
	}
	check := func(rel string, want ...string) {
		t.Helper()
		g := subsOf(rel)
		if strings.Join(g, ",") != strings.Join(want, ",") {
			t.Errorf("subs of %s = %v, want %v", rel, g, want)
		}
	}
	check("A Movie (2010)/A Movie (2010).mkv",
		"A Movie (2010).ro.srt=ro",
		"Subs/A Movie (2010).en.forced.srt=en+forced",
		"Subs/A Movie (2010)/2_English.srt=en",
		"Subs/A Movie (2010)/3_Romanian.srt=ro")
	check("Scene.Name.2015.1080p.BluRay.x264-GRP.mkv", "Scene.Name.2015.1080p.BluRay.x264-GRP.eng.srt=en")
	check("Two/Movie.mkv", "Movie.ro.srt=ro")
	check("Two/Movie.Part2.mkv", "Movie.Part2.en.srt=en")

	if s := sc.Status(); s.Scanning || s.FilesSeen != 14 || s.ProbeErrors != 12 || s.Unmatched != 2 {
		t.Errorf("status = %+v", s)
	}

	// Progress on a file that is then removed survives, ids are stable across scans.
	a := got["A Movie (2010)/A Movie (2010).mkv"]
	if _, err := st.UpdateProgress(ctx, a.ID, func(p progress.State) progress.State {
		return progress.Apply(p, 5*60000, 100*60000, 0, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(a.Path, a.Path+".bak"); err != nil {
		t.Fatal(err)
	}
	touch(t, movies, "Two/New.Film.2022.mkv")
	if _, err := sc.ScanAll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetItem(ctx, a.ID); err != store.ErrNotFound {
		t.Errorf("removed file still listed: %v", err)
	}
	if p, _ := st.Progress(ctx, a.ID); p.PositionMs != 5*60000 {
		t.Errorf("progress of removed file lost: %+v", p)
	}
	if _, err := st.GetItem(ctx, ItemID(filepath.Join(movies, "Two", "New.Film.2022.mkv"))); err != nil {
		t.Errorf("new file not added: %v", err)
	}
	if err := os.Rename(a.Path+".bak", a.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.ScanAll(ctx); err != nil {
		t.Fatal(err)
	}
	if cw, _ := st.ContinueWatching(ctx, 10); len(cw) != 1 || cw[0].ID != a.ID {
		t.Errorf("returning file lost its progress: %v", cw)
	}

	// An unreachable library is never emptied.
	gone := []config.Library{{Name: "Movies", Type: "movies", Path: filepath.Join(root, "unplugged")}}
	sc2 := NewScanner(st, gone, "x", quietLog())
	if _, err := sc2.ScanAll(ctx); err == nil {
		t.Error("expected an error for an unreadable library")
	}
	if ms, _ := st.ListMovies(ctx, "title", "", 0); len(ms) != len(wantMovies)+1 {
		t.Errorf("unreachable library removed items: %d left", len(ms))
	}
}

func TestIDs(t *testing.T) {
	a := ItemID(`C:\Media\Movies\Film (2020)\Film (2020).mkv`)
	if len(a) != 16 {
		t.Fatalf("id length %d", len(a))
	}
	if ItemID(`C:\Media\Movies\Film (2020)\Film (2020).mkv`) != a {
		t.Error("id not stable")
	}
	if ShowID(`C:\Media\Shows\X`) == ItemID(`C:\Media\Shows\X`) {
		t.Error("show and item ids collide")
	}
	// NFC vs NFD spellings of "ș" are the same file name.
	if NormPath("/m/\u0219tiin\u021ba") != NormPath("/m/s\u0326tiint\u0326a") {
		t.Error("NFD/NFC paths differ")
	}
}

// TestScanGeneratedMedia scans testdata/media (run testdata/generate.ps1 first).
func TestScanGeneratedMedia(t *testing.T) {
	media, _ := filepath.Abs(filepath.Join("..", "..", "..", "testdata", "media"))
	if _, err := os.Stat(filepath.Join(media, "Movies")); err != nil {
		t.Skip("testdata/media not generated; run testdata/generate.ps1")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not on PATH")
	}
	st := openStore(t)
	libs := []config.Library{
		{Name: "Movies", Type: "movies", Path: filepath.Join(media, "Movies")},
		{Name: "Shows", Type: "shows", Path: filepath.Join(media, "Shows")},
	}
	sc := NewScanner(st, libs, ffprobe, quietLog())
	ctx := context.Background()
	if _, err := sc.ScanAll(ctx); err != nil {
		t.Fatal(err)
	}
	if s := sc.Status(); s.ProbeErrors != 0 || s.Probed != 8 {
		t.Errorf("status = %+v", s)
	}
	movies, _ := st.ListMovies(ctx, "title", "", 0)
	var mt []string
	for _, m := range movies {
		mt = append(mt, m.Title+"|"+itoa(m.Year)+"|"+itoa64(m.RuntimeMs/1000))
	}
	want := "Big Test Movie|2021|120,HDR Sample|2020|30,Irregular Keys|2019|60,Știința Ușoară|2018|30"
	if strings.Join(mt, ",") != want {
		t.Errorf("movies = %v\nwant    %s", mt, want)
	}
	shows, _ := st.ListShows(ctx, "title", 0)
	if len(shows) != 1 || shows[0].Title != "Test Show" || shows[0].Year != 2020 || shows[0].EpisodeCount != 3 {
		t.Fatalf("shows = %+v", shows)
	}
	eps, _ := st.Episodes(ctx, shows[0].ID)
	var et []string
	for _, e := range eps {
		et = append(et, "S"+itoa(e.Season)+"E"+itoa(e.Episode)+"-"+itoa(e.EpisodeEnd)+" "+e.Title)
	}
	if strings.Join(et, ",") != "S1E1-0 Pilot,S1E2-0 Second,S1E3-4 Double,S0E1-0 Special" {
		t.Errorf("episodes = %v", et)
	}
	pilot := eps[0]
	if m, _ := st.Markers(ctx, pilot.ID); len(m) != 2 || m[0].Kind != "intro" || m[1].Kind != "credits" || m[1].StartMs != 150000 {
		t.Errorf("pilot markers = %+v", m)
	}
	ps, _ := st.Subtitles(ctx, pilot.ID)
	if len(ps) != 2 || ps[0].Source != "embedded" || ps[0].Codec != "subrip" || ps[1].Source != "external" || ps[1].Language != "ro" {
		t.Errorf("pilot subtitles = %+v", ps)
	}
	if um := sc.Unmatched(); len(um) != 1 || filepath.Base(um[0].Path) != "random clip.mkv" {
		t.Errorf("unmatched = %+v", um)
	}
	big := movies[0]
	bs, _ := st.Subtitles(ctx, big.ID)
	if len(bs) != 2 || bs[0].Language != "en" || !bs[0].Forced || bs[1].Language != "ro" {
		t.Errorf("big movie subtitles = %+v", bs)
	}

	// A second scan re-probes nothing.
	sc2 := NewScanner(st, libs, ffprobe, quietLog())
	if _, err := sc2.ScanAll(ctx); err != nil {
		t.Fatal(err)
	}
	if s := sc2.Status(); s.Probed != 0 {
		t.Errorf("rescan probed %d files", s.Probed)
	}
}

func itoa(n int) string     { return strconv.Itoa(n) }
func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// A movies library with TV mixed in (PLAN.md 3.2): files with an S01E02/1x02
// code become episodes, grouped into shows by name wherever they sit.
func TestScanMixedMovieLibrary(t *testing.T) {
	root := t.TempDir()
	touch(t, root,
		"Big.Buck.Bunny.2008.1080p.WEB-DL.mkv",
		"Tears.of.Steel.2012.1080p.BluRay/Tears.of.Steel.2012.1080p.BluRay.x264.mkv",
		"Pioneer.One.S02E02.Fallen.1080p.AMZN.WEB-DL.mkv",
		"Pioneer.One.S02E03.Live.Free.1080p.AMZN.WEB-DL.mkv",
		"Pioneer One (2010)/Season 2/Pioneer.One.S02E04.mkv",
		"Pioneer One (2010)/poster.jpg",
		"The.Beverly.Hillbillies.S01.1080p.WEB-DL/The.Beverly.Hillbillies.S01E01.The.Clampetts.Strike.Oil.mkv",
		"The.Beverly.Hillbillies.S01.1080p.WEB-DL/The.Beverly.Hillbillies.S01E02.Getting.Settled.mkv",
		"Some Show/S01E05.mkv",
		"S01E09.mkv",
		"Space Saga Episode IV (1977).mkv",
	)
	st := openStore(t)
	sc := NewScanner(st, []config.Library{{Name: "Mixed", Type: "movies", Path: root}}, filepath.Join(root, "no-ffprobe.exe"), quietLog())
	ctx := context.Background()
	if _, err := sc.ScanAll(ctx); err != nil {
		t.Fatal(err)
	}

	items, _ := st.AllItems(ctx)
	kinds := map[string]string{}
	showOf := map[string]string{}
	for _, it := range items {
		rel := filepath.ToSlash(it.RelPath)
		kinds[rel] = it.Kind
		showOf[rel] = it.ShowID
	}
	for _, rel := range []string{"Big.Buck.Bunny.2008.1080p.WEB-DL.mkv", "Tears.of.Steel.2012.1080p.BluRay/Tears.of.Steel.2012.1080p.BluRay.x264.mkv", "Space Saga Episode IV (1977).mkv"} {
		if kinds[rel] != "movie" {
			t.Errorf("%s: kind %q, want movie", rel, kinds[rel])
		}
	}
	if _, ok := kinds["S01E09.mkv"]; ok {
		t.Error("S01E09.mkv has no show name and should be unmatched")
	}

	shows, err := st.ListShows(ctx, "title", 0)
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]*store.Show{}
	for _, sh := range shows {
		byTitle[sh.Title] = sh
	}
	want := map[string]struct {
		year     int
		folder   string
		episodes int
	}{
		"Pioneer One":             {2010, "Pioneer One (2010)", 3},
		"The Beverly Hillbillies": {0, "The.Beverly.Hillbillies.S01.1080p.WEB-DL", 2},
		"Some Show":               {0, "Some Show", 1},
	}
	if len(shows) != len(want) {
		var names []string
		for k := range byTitle {
			names = append(names, k)
		}
		t.Fatalf("got shows %q, want %d", names, len(want))
	}
	for title, w := range want {
		sh := byTitle[title]
		if sh == nil {
			t.Errorf("missing show %q", title)
			continue
		}
		if sh.Year != w.year || sh.Path != filepath.Join(root, w.folder) {
			t.Errorf("%s: year %d path %s, want %d %s", title, sh.Year, sh.Path, w.year, w.folder)
		}
		eps, _ := st.Episodes(ctx, sh.ID)
		if len(eps) != w.episodes {
			t.Errorf("%s: %d episodes, want %d", title, len(eps), w.episodes)
		}
	}
	if showOf["Pioneer.One.S02E02.Fallen.1080p.AMZN.WEB-DL.mkv"] != showOf["Pioneer One (2010)/Season 2/Pioneer.One.S02E04.mkv"] {
		t.Error("loose Pioneer One episodes and its folder should be one show")
	}
	if n := len(sc.Unmatched()); n != 1 {
		t.Errorf("unmatched = %d, want 1", n)
	}
}
