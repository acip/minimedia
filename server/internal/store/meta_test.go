package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// A Phase 1 (schema version 1) database upgrades in place and keeps its data.
func TestMigrateFromVersion1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO meta(key, value) VALUES ('schema_version', '1'), ('server_id', 'abc');
	  INSERT INTO items (id, library, kind, path, rel_path, size, mtime, title, sort_title, added_at)
	    VALUES ('i1', 'M', 'movie', 'C:\m\a.mkv', 'a.mkv', 1, 1, 'Old Title', 'old title', 1);
	  INSERT INTO progress (item_id, position_ms) VALUES ('i1', 300000);`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer func() { s.Close() }() // s is reopened below
	if v, _ := s.Meta("schema_version"); v != "2" {
		t.Errorf("schema_version = %q", v)
	}
	if id, _ := s.ServerID(); id != "abc" {
		t.Errorf("server id lost: %q", id)
	}
	ctx := context.Background()
	it, err := s.GetItem(ctx, "i1")
	if err != nil || it.Title != "Old Title" || it.Progress.PositionMs != 300000 {
		t.Fatalf("item after migration = %+v, %v", it, err)
	}
	// The new columns work.
	if err := s.SetItemMeta(ctx, "i1", &Meta{TMDBID: 5, Title: "New Title", SortKey: "new title", Year: 1999}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if it, _ := s.GetItem(ctx, "i1"); it.Title != "New Title" || it.FileTitle != "Old Title" || it.Year != 1999 || it.TMDBID != 5 {
		t.Errorf("meta not applied: %+v", it)
	}
	// Reopening an up-to-date database is a no-op.
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataSurvivesRescansAndHintChanges(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	up := func(hint int64, title string) {
		t.Helper()
		tx, _ := s.Begin(ctx)
		if err := tx.UpsertItem(&Item{ID: "m", Library: "M", Kind: "movie", Path: "p", RelPath: "p", Title: title,
			SortTitle: title, AddedAt: 1, TMDBHint: hint}); err != nil {
			t.Fatal(err)
		}
		tx.Commit()
	}
	up(0, "file title")
	if err := s.SetItemMeta(ctx, "m", &Meta{TMDBID: 7, Title: "TMDB Title", SortKey: "tmdb title", Overview: "o", Genres: []string{"Drama"}, Rating: 6.6}, time.Now()); err != nil {
		t.Fatal(err)
	}
	up(0, "file title") // a rescan
	it, _ := s.GetItem(ctx, "m")
	if it.Title != "TMDB Title" || it.Overview != "o" || len(it.Genres) != 1 || it.Rating != 6.6 {
		t.Errorf("rescan clobbered metadata: %+v", it)
	}
	if n, _ := s.MoviesNeedingMeta(ctx, time.Now().Add(-time.Hour)); len(n) != 0 {
		t.Errorf("matched movie still pending: %+v", n)
	}
	up(603, "file title") // tmdb.txt appears → looked up again
	if n, _ := s.MoviesNeedingMeta(ctx, time.Now().Add(-time.Hour)); len(n) != 1 || n[0].Hint != 603 {
		t.Errorf("hint change did not requeue: %+v", n)
	}

	// Sorting and lists use the TMDB title.
	tx, _ := s.Begin(ctx)
	tx.UpsertItem(&Item{ID: "z", Library: "M", Kind: "movie", Path: "z", RelPath: "z", Title: "Middle", SortTitle: "middle", AddedAt: 1})
	tx.Commit()
	ms, _ := s.ListMovies(ctx, "title", "", 0)
	if len(ms) != 2 || ms[0].ID != "z" || ms[1].ID != "m" {
		t.Errorf("sort order = %s, %s (want Middle before TMDB Title)", ms[0].Title, ms[1].Title)
	}
}

func TestImagesPriorityAndPurge(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.ReplaceImages(ctx, "m", "tmdb", []Image{{Kind: "poster", Path: "/t.jpg", Version: "t"}, {Kind: "logo", Path: "/l.png", Version: "l"}}); err != nil {
		t.Fatal(err)
	}
	tx, _ := s.Begin(ctx)
	tx.ReplaceImages("m", "local", []Image{{Kind: "poster", Path: `C:\m\poster.jpg`, Version: "L"}})
	tx.Commit()
	if im, _ := s.BestImage(ctx, "m", "poster"); im.Source != "local" {
		t.Errorf("best poster = %+v", im)
	}
	refs, _ := s.ImageRefs(ctx, []string{"m", "m", "", "other"})
	if refs["m"]["poster"].Version != "L" || refs["m"]["logo"].Version != "l" || len(refs) != 1 {
		t.Errorf("refs = %+v", refs)
	}
	// The local file disappears → TMDB again.
	tx, _ = s.Begin(ctx)
	tx.ReplaceImages("m", "local", nil)
	tx.Commit()
	if im, _ := s.BestImage(ctx, "m", "poster"); im.Source != "tmdb" {
		t.Errorf("after local removal = %+v", im)
	}
	if _, err := s.BestImage(ctx, "m", "still"); err != ErrNotFound {
		t.Errorf("missing kind: %v", err)
	}
	// Images of owners that no longer exist are purged.
	if err := s.PurgeOrphans(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BestImage(ctx, "m", "logo"); err != ErrNotFound {
		t.Errorf("orphan image kept: %v", err)
	}
}

func TestShowPrefs(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	p, err := s.ShowPrefs(ctx, "sh")
	if err != nil || p.Set || p.SubtitlesMode != "off" {
		t.Errorf("default = %+v %v", p, err)
	}
	if err := s.SetShowPrefs(ctx, "sh", ShowPrefs{AudioLanguage: "en", SubtitlesMode: "forced_only"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.ShowPrefs(ctx, "sh"); !p.Set || p.AudioLanguage != "en" || p.SubtitlesMode != "forced_only" {
		t.Errorf("saved = %+v", p)
	}
}
