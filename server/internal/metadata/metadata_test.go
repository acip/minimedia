package metadata

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/acip/minimedia/server/internal/store"
)

// fakeTMDB answers the endpoints the fetcher uses and records requests.
type fakeTMDB struct {
	mu       sync.Mutex
	requests []string
	auth     []string
}

func (f *fakeTMDB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.Path+"?"+r.URL.RawQuery)
	f.auth = append(f.auth, r.Header.Get("Authorization")+"|"+r.URL.Query().Get("api_key"))
	f.mu.Unlock()
	q := r.URL.Query()
	lang := q.Get("language")
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch r.URL.Path {
	case "/search/movie":
		switch q.Get("query") {
		case "Big Test Movie":
			if q.Get("year") == "2021" {
				write(map[string]any{"results": []map[string]any{
					{"id": 11, "title": "Big Test Movie 2", "release_date": "2021-05-01"},
					{"id": 10, "title": "Big Test Movie", "release_date": "2021-03-01"},
				}})
				return
			}
		case "Remake":
			write(map[string]any{"results": []map[string]any{{"id": 30, "title": "Remake", "release_date": "1960-01-01"}}})
			return
		}
		write(map[string]any{"results": []any{}})
	case "/movie/10":
		ov := "A big test."
		if lang == "ro-RO" {
			ov = "" // missing translation → English fallback
		}
		write(map[string]any{"id": 10, "title": "Big Test Movie", "overview": ov, "genres": []map[string]any{{"name": "Drama"}},
			"vote_average": 7.46, "vote_count": 100, "release_date": "2021-03-01",
			"poster_path": "/poster10.jpg", "backdrop_path": "/backdrop10.jpg",
			"images": map[string]any{"logos": []map[string]any{
				{"file_path": "/logo-de.png", "iso_639_1": "de", "vote_average": 9},
				{"file_path": "/logo-null.png", "iso_639_1": nil, "vote_average": 8},
				{"file_path": "/logo-en.png", "iso_639_1": "en", "vote_average": 5},
				{"file_path": "/logo-en2.svg", "iso_639_1": "en", "vote_average": 9},
			}}})
	case "/movie/603":
		write(map[string]any{"id": 603, "title": "The General", "overview": "A train chase.", "vote_count": 0})
	case "/search/tv":
		if q.Get("query") == "Test Show" {
			write(map[string]any{"results": []map[string]any{{"id": 20, "name": "Test Show", "first_air_date": "2020-01-01"}}})
			return
		}
		write(map[string]any{"results": []any{}})
	case "/tv/20":
		write(map[string]any{"id": 20, "name": "Test Show", "overview": "A show.", "first_air_date": "2020-01-01",
			"genres": []map[string]any{{"name": "Comedy"}}, "vote_average": 8, "vote_count": 3,
			"poster_path": "/tvposter.jpg", "backdrop_path": "/tvback.jpg"})
	case "/tv/20/season/1":
		write(map[string]any{"episodes": []map[string]any{
			{"id": 201, "episode_number": 1, "name": "Pilot", "overview": "First.", "still_path": "/s1e1.jpg", "vote_average": 7},
			{"id": 202, "episode_number": 2, "name": "Episode 2", "overview": "Second."},
			{"id": 203, "episode_number": 3, "name": "Part One"},
			{"id": 204, "episode_number": 4, "name": "Part Two"},
		}})
	default:
		http.NotFound(w, r)
	}
}

func seed(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	tx, _ := st.Begin(ctx)
	now := time.Now().UnixMilli()
	movie := func(id, title string, year int, hint int64) {
		if err := tx.UpsertItem(&store.Item{ID: id, Library: "M", Kind: "movie", Path: `C:\m\` + id + `.mkv`, RelPath: id + ".mkv",
			Title: title, SortTitle: strings.ToLower(title), Year: year, AddedAt: now, TMDBHint: hint}); err != nil {
			t.Fatal(err)
		}
	}
	movie("big", "Big Test Movie", 2021, 0)
	movie("hinted", "Whatever Name", 0, 603)
	movie("nomatch", "Nothing Like This", 2000, 0)
	movie("remake", "Remake", 2020, 0)
	if err := tx.UpsertShow(&store.Show{ID: "show", Library: "S", Path: `C:\s\Test Show`, Title: "Test Show", SortTitle: "test show", AddedAt: now}); err != nil {
		t.Fatal(err)
	}
	ep := func(id string, s, e, end int, title string) {
		if err := tx.UpsertItem(&store.Item{ID: id, Library: "S", Kind: "episode", Path: `C:\s\` + id + `.mkv`, RelPath: id,
			Title: title, SortTitle: title, ShowID: "show", Season: s, Episode: e, EpisodeEnd: end, AddedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	ep("e1", 1, 1, 0, "")
	ep("e2", 1, 2, 0, "From File")
	ep("e34", 1, 3, 4, "")
	ep("e9", 1, 9, 0, "")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return st, ctx
}

func newTestFetcher(t *testing.T, st *store.Store, key, lang string) (*Fetcher, *fakeTMDB) {
	fake := &fakeTMDB{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	c := NewClient(key, lang, filepath.Join(t.TempDir(), "cache"))
	c.Base = srv.URL
	c.gap = 0
	return NewFetcher(st, c, slog.New(slog.NewTextHandler(io.Discard, nil))), fake
}

func TestFetchMoviesAndShows(t *testing.T) {
	st, ctx := seed(t)
	f, fake := newTestFetcher(t, st, "0123456789abcdef0123456789abcdef", "ro-RO")
	if err := f.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	big, _ := st.GetItem(ctx, "big")
	if big.TMDBID != 10 || big.Title != "Big Test Movie" || big.Overview != "A big test." || big.Rating != 7.5 ||
		len(big.Genres) != 1 || big.Genres[0] != "Drama" || big.FileTitle != "Big Test Movie" {
		t.Errorf("big = %+v", big)
	}
	logo, err := st.BestImage(ctx, "big", "logo")
	if err != nil || logo.Path != "/logo-en.png" || logo.Source != "tmdb" {
		t.Errorf("logo = %+v %v (want the English one: no Romanian logo, English beats language-neutral, SVG skipped)", logo, err)
	}
	if p, _ := st.BestImage(ctx, "big", "poster"); p == nil || p.Path != "/poster10.jpg" {
		t.Errorf("poster = %+v", p)
	}

	hinted, _ := st.GetItem(ctx, "hinted")
	if hinted.TMDBID != 603 || hinted.Title != "The General" || hinted.FileTitle != "Whatever Name" || hinted.Rating != 0 {
		t.Errorf("tmdb.txt hint not used: %+v", hinted)
	}
	if n, _ := st.GetItem(ctx, "nomatch"); n.TMDBID != 0 || n.Title != "Nothing Like This" {
		t.Errorf("nomatch = %+v", n)
	}
	if r, _ := st.GetItem(ctx, "remake"); r.TMDBID != 0 {
		t.Errorf("a 1960 film matched a 2020 file: %+v", r)
	}

	sh, _ := st.GetShow(ctx, "show")
	if sh.TMDBID != 20 || sh.Year != 2020 || sh.Overview != "A show." || sh.Rating != 8 {
		t.Errorf("show = %+v", sh)
	}
	eps, _ := st.Episodes(ctx, "show")
	titles := map[string]string{}
	for _, e := range eps {
		titles[e.ID] = e.Title + "|" + e.Overview
	}
	want := map[string]string{
		"e1":  "Pilot|First.",
		"e2":  "From File|Second.", // "Episode 2" is a placeholder; the file title stays
		"e34": "Part One / Part Two|",
		"e9":  "|", // not on TMDB
	}
	for id, w := range want {
		if titles[id] != w {
			t.Errorf("%s = %q, want %q", id, titles[id], w)
		}
	}
	if s, _ := st.BestImage(ctx, "e1", "still"); s == nil || s.Path != "/s1e1.jpg" {
		t.Errorf("still = %+v", s)
	}
	c, _ := st.MetaCounts(ctx)
	if c.Matched != 3 || c.Unmatched != 2 || c.Pending != 0 {
		t.Errorf("counts = %+v", c)
	}
	for _, a := range fake.auth {
		if !strings.HasSuffix(a, "|0123456789abcdef0123456789abcdef") || strings.HasPrefix(a, "Bearer") {
			t.Errorf("v3 key must go in api_key: %q", a)
		}
	}

	// A second run does nothing: everything was looked up.
	n := len(fake.requests)
	if err := f.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != n {
		t.Errorf("second run made %d requests", len(fake.requests)-n)
	}

	// A new episode of a matched show is filled in from the (cached) season.
	tx, _ := st.Begin(ctx)
	tx.UpsertItem(&store.Item{ID: "e2b", Library: "S", Kind: "episode", Path: `C:\s\e2b.mkv`, RelPath: "e2b", ShowID: "show",
		Season: 1, Episode: 1, AddedAt: time.Now().UnixMilli()})
	tx.Commit()
	if err := f.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if e, _ := st.GetItem(ctx, "e2b"); e.Title != "Pilot" {
		t.Errorf("new episode = %+v", e)
	}
	if s := f.Status(ctx); !s.Enabled || s.Running || s.LastRunAt == nil || s.LastError != "" {
		t.Errorf("status = %+v", s)
	}
}

func TestBearerTokenAndAuthError(t *testing.T) {
	st, ctx := seed(t)
	token := "eyJhbGciOiJIUzI1NiJ9." + strings.Repeat("x", 100)
	f, fake := newTestFetcher(t, st, token, "en-US")
	if err := f.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fake.auth[0], "Bearer "+token+"|") || strings.HasSuffix(fake.auth[0], token) {
		t.Errorf("v4 token must be a bearer header only: %q", fake.auth[0])
	}

	st2, ctx2 := seed(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient("bad", "en-US", t.TempDir())
	c.Base, c.gap = srv.URL, 0
	f2 := NewFetcher(st2, c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := f2.RunOnce(ctx2); err != ErrAuth {
		t.Errorf("err = %v, want ErrAuth", err)
	}
	if cnt, _ := st2.MetaCounts(ctx2); cnt.Pending != 5 {
		t.Errorf("items must stay pending after an auth error: %+v", cnt)
	}
	if s := f2.Status(ctx2); !strings.Contains(s.LastError, "API key") {
		t.Errorf("status = %+v", s)
	}
}

func TestBestMatch(t *testing.T) {
	r := []searchResult{
		{ID: 1, Title: "Ben-Hur", ReleaseDate: "1907-12-07"},
		{ID: 2, Title: "Ben-Hur", ReleaseDate: "1925-12-30"},
		{ID: 3, Title: "Ben-Hur: A Tale of the Christ", ReleaseDate: "1928-03-01"},
	}
	if got := bestMatch(r, "Ben-Hur", 1925); got != 2 {
		t.Errorf("Ben-Hur 1925 → %d", got)
	}
	if got := bestMatch(r, "Ben-Hur", 0); got != 1 {
		t.Errorf("Ben-Hur (no year) → %d, want TMDB's first exact match", got)
	}
	if got := bestMatch(r, "Ben-Hur", 1926); got != 2 {
		t.Errorf("Ben-Hur 1926 (off by one) → %d", got)
	}
	if got := bestMatch([]searchResult{{ID: 9, Title: "Știința Ușoară"}}, "Stiinta Usoara", 2018); got != 9 {
		t.Errorf("diacritics/unknown date → %d", got)
	}
}
