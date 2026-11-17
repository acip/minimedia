package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/acip/minimedia/server/internal/progress"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Date Ștefan #1 50%")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "minimedia.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database not created at the expected path: %v", err)
	}
	return s
}

func TestOpenReopenAndServerID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id1, err := s.ServerID()
	if err != nil || len(id1) != 32 {
		t.Fatalf("server id %q %v", id1, err)
	}
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q %v", mode, err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if id2, _ := s.ServerID(); id2 != id1 {
		t.Errorf("server id changed: %s -> %s", id1, id2)
	}
}

func TestItemLifecycleKeepsProgress(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Now()

	tx, _ := s.Begin(ctx)
	must(t, tx.UpsertShow(&Show{ID: "show1", Library: "Shows", Path: `X:\Shows\S`, Title: "S", SortTitle: "s", AddedAt: 1}))
	must(t, tx.UpsertItem(&Item{ID: "m1", Library: "Movies", Kind: "movie", Path: "p", RelPath: "m.mkv", Size: 1, MtimeMs: 2,
		Title: "Movie", SortTitle: "movie", Year: 2020, AddedAt: 100, ProbeJSON: `{"a":1}`, RuntimeMs: 5000, Genres: []string{"Drama"}}))
	must(t, tx.UpsertItem(&Item{ID: "e1", Library: "Shows", Kind: "episode", Path: "p2", RelPath: "e.mkv", Title: "Pilot",
		SortTitle: "pilot", ShowID: "show1", Season: 1, Episode: 1, EpisodeEnd: 2, AddedAt: 200}))
	must(t, tx.ReplaceSubtitles("m1", "external", []Subtitle{{ID: "s1", Path: "a.srt", Language: "ro", Codec: "srt"}}))
	must(t, tx.ReplaceSubtitles("m1", "embedded", []Subtitle{{ID: "s2", StreamIndex: 2, Language: "en", Label: "English", Codec: "subrip"}}))
	must(t, tx.ReplaceMarkers("e1", []Marker{{"intro", 0, 20000}, {"credits", 150000, 180000}}))
	must(t, tx.Commit())

	// Re-upsert without probe must keep probe/runtime/added_at.
	tx, _ = s.Begin(ctx)
	must(t, tx.UpsertItem(&Item{ID: "m1", Library: "Movies", Kind: "movie", Path: "p", RelPath: "m.mkv", Size: 1, MtimeMs: 2,
		Title: "Movie", SortTitle: "movie", Year: 2020, AddedAt: 999}))
	must(t, tx.Commit())
	it, err := s.GetItem(ctx, "m1")
	if err != nil || it.ProbeJSON != `{"a":1}` || it.RuntimeMs != 5000 || it.AddedAt != 100 || it.Genres[0] != "Drama" {
		t.Fatalf("item = %+v, %v", it, err)
	}
	ep, _ := s.GetItem(ctx, "e1")
	if ep.ShowTitle != "S" || ep.Season != 1 || ep.EpisodeEnd != 2 {
		t.Errorf("episode = %+v", ep)
	}
	subs, _ := s.Subtitles(ctx, "m1")
	if len(subs) != 2 || subs[0].ID != "s2" || subs[0].StreamIndex != 2 || subs[1].Source != "external" || subs[1].Path != "a.srt" {
		t.Errorf("subs = %+v", subs)
	}
	if c, _ := s.CreditsStart(ctx, "e1"); c != 150000 {
		t.Errorf("credits = %d", c)
	}
	if movies, _ := s.ListMovies(ctx, "title", "drama", 0); len(movies) != 1 {
		t.Errorf("genre filter: %d", len(movies))
	}

	// Progress survives deletion and comes back with the file.
	if _, err := s.UpdateProgress(ctx, "m1", func(p progress.State) progress.State {
		return progress.Apply(p, 3*60000, 5000*60, 0, now)
	}); err != nil {
		t.Fatal(err)
	}
	tx, _ = s.Begin(ctx)
	must(t, tx.DeleteItems([]string{"m1"}, now))
	must(t, tx.Commit())
	if _, err := s.GetItem(ctx, "m1"); err != ErrNotFound {
		t.Errorf("deleted item still there: %v", err)
	}
	must(t, s.PurgeOrphans(ctx, now.Add(24*time.Hour)))
	if p, _ := s.Progress(ctx, "m1"); p.PositionMs != 3*60000 {
		t.Errorf("progress lost after 1 day: %+v", p)
	}
	tx, _ = s.Begin(ctx)
	must(t, tx.UpsertItem(&Item{ID: "m1", Library: "Movies", Kind: "movie", Path: "p", RelPath: "m.mkv", Title: "Movie", SortTitle: "movie", AddedAt: 1}))
	must(t, tx.Commit())
	if cw, _ := s.ContinueWatching(ctx, 10); len(cw) != 1 || cw[0].ID != "m1" {
		t.Errorf("continue watching = %v", cw)
	}

	// After 30 days missing the progress is purged.
	tx, _ = s.Begin(ctx)
	must(t, tx.DeleteItems([]string{"m1"}, now))
	must(t, tx.Commit())
	must(t, s.PurgeOrphans(ctx, now.Add(31*24*time.Hour)))
	if p, _ := s.Progress(ctx, "m1"); p.PositionMs != 0 {
		t.Errorf("orphaned progress not purged: %+v", p)
	}

	// Shows without episodes are purged.
	tx, _ = s.Begin(ctx)
	must(t, tx.DeleteItems([]string{"e1"}, now))
	must(t, tx.Commit())
	must(t, s.PurgeOrphans(ctx, now))
	if _, err := s.GetShow(ctx, "show1"); err != ErrNotFound {
		t.Errorf("empty show not purged: %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestProbedItems(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	tx, _ := st.Begin(ctx)
	for i, id := range []string{"a", "b", "c"} {
		it := &Item{ID: id, Library: "L", Kind: "movie", Path: "/m/" + id, RelPath: id, Title: id, SortTitle: id, AddedAt: int64(i)}
		if id != "b" {
			it.ProbeJSON = `{"streams":[]}`
		}
		if err := tx.UpsertItem(it); err != nil {
			t.Fatal(err)
		}
	}
	tx.Commit()
	items, err := st.ProbedItems(ctx)
	if err != nil || len(items) != 2 || items[0].ID != "c" || items[1].ID != "a" || items[0].ProbeJSON == "" {
		t.Fatalf("got %v, %v", items, err)
	}
}
