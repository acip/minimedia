package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"

	"github.com/acip/minimedia/server/internal/config"
	"github.com/acip/minimedia/server/internal/images"
	"github.com/acip/minimedia/server/internal/library"
	"github.com/acip/minimedia/server/internal/store"
	"github.com/acip/minimedia/server/internal/trickplay"
)

type env struct {
	t       *testing.T
	srv     *httptest.Server
	movies  string
	shows   string
	movie   []byte // contents of the direct-play test file
	now     time.Time
	api     *Server
	dataDir string
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{t: t, movies: filepath.Join(root, "Movies"), shows: filepath.Join(root, "Shows"), now: time.Now()}
	e.movie = make([]byte, 5000)
	for i := range e.movie {
		e.movie[i] = byte(i * 7)
	}
	write(t, filepath.Join(e.movies, "Știința Ușoară (2018)", "Știința Ușoară (2018).mkv"), e.movie)
	ro, _ := charmap.Windows1250.NewEncoder().Bytes([]byte("1\r\n00:00:01,000 --> 00:00:02,000\r\n„Ştiinţa e uşoară”\r\n"))
	write(t, filepath.Join(e.movies, "Știința Ușoară (2018)", "Știința Ușoară (2018).ro.srt"), ro)
	write(t, filepath.Join(e.movies, "Știința Ușoară (2018)", "Știința Ușoară (2018).en.srt"), []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"))
	write(t, filepath.Join(e.movies, "Știința Ușoară (2018)", "Știința Ușoară (2018).en.forced.srt"), []byte("x"))
	write(t, filepath.Join(e.movies, "Alpha.2001.1080p.BluRay.x264.mp4"), []byte("a"))
	write(t, filepath.Join(e.movies, "Zaza (1923).avi"), []byte("z"))
	for _, ep := range []string{"Show - S01E01 - One.mkv", "Show - S01E02 - Two.mkv", "Show - S01E03 - Three.mkv"} {
		write(t, filepath.Join(e.shows, "My Show (2019)", "Season 01", ep), []byte("e"))
	}
	write(t, filepath.Join(e.shows, "My Show (2019)", "Season 02", "Show - S02E01 - Four.mkv"), []byte("e"))
	write(t, filepath.Join(e.shows, "My Show (2019)", "Specials", "Show - S00E01 - Bonus.mkv"), []byte("e"))
	write(t, filepath.Join(e.shows, "My Show (2019)", "notes.mkv"), []byte("e"))

	st, err := store.Open(filepath.Join(root, "data", "minimedia.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sc := library.NewScanner(st, []config.Library{
		{Name: "Movies", Type: "movies", Path: e.movies},
		{Name: "Shows", Type: "shows", Path: e.shows},
	}, filepath.Join(root, "no-ffprobe"), log)
	if _, err := sc.ScanAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.api = New(Options{Name: "Test", Version: "1.0", ServerID: "sid", DataDir: filepath.Join(root, "data"),
		FallbackEncoding: charmap.Windows1250, PreferredLanguages: []string{"ro"},
		Images:    images.New(st, filepath.Join(root, "data"), "", log),
		Trickplay: trickplay.New(st, filepath.Join(root, "data"), "", trickplay.Options{Enabled: true, IntervalSeconds: 10, Width: 320}, log),
	}, st, sc, log)
	e.dataDir = filepath.Join(root, "data")
	e.api.now = func() time.Time { return e.now }
	e.srv = httptest.NewServer(e.api.Handler())
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) do(method, path string, body any, hdr map[string]string) *http.Response {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

func (e *env) json(method, path string, body any, wantStatus int, out any) {
	e.t.Helper()
	resp := e.do(method, path, body, nil)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		e.t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, wantStatus, data)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		e.t.Fatalf("%s %s content-type %q", method, path, resp.Header.Get("Content-Type"))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			e.t.Fatalf("%s %s: %v: %s", method, path, err, data)
		}
	}
}

type itemResp struct {
	Item      Summary `json:"item"`
	Subtitles []struct {
		ID       string  `json:"id"`
		Language string  `json:"language"`
		Label    string  `json:"label"`
		Forced   bool    `json:"forced"`
		Embedded bool    `json:"embedded"`
		URL      *string `json:"url"`
		MIME     string  `json:"mime"`
	} `json:"subtitles"`
	Progress    progressOut `json:"progress"`
	NextEpisode *Summary    `json:"nextEpisode"`
	FileName    string      `json:"fileName"`
}

func TestInfoAndErrors(t *testing.T) {
	e := setup(t)
	var info map[string]string
	e.json("GET", "/api/info", nil, 200, &info)
	if info["name"] != "Test" || info["serverId"] != "sid" || info["version"] != "1.0" || info["os"] == "" {
		t.Errorf("info = %v", info)
	}
	var er map[string]string
	e.json("GET", "/api/items/doesnotexist", nil, 404, &er)
	if er["error"] == "" {
		t.Errorf("error body = %v", er)
	}
	e.json("GET", "/api/nope", nil, 404, &er)
	e.json("POST", "/api/info", nil, 405, nil)
	e.json("PUT", "/api/items/x/progress", map[string]any{"durationMs": 1}, 400, nil)
	var scan map[string]string
	e.json("POST", "/api/library/scan", nil, 202, &scan)
	var unmatched []library.Unmatched
	e.json("GET", "/api/library/unmatched", nil, 200, &unmatched)
	if len(unmatched) != 1 || filepath.Base(unmatched[0].Path) != "notes.mkv" {
		t.Errorf("unmatched = %+v", unmatched)
	}
	var status map[string]any
	e.json("GET", "/api/library/status", nil, 200, &status)
	if _, ok := status["scan"]; !ok {
		t.Errorf("status = %v", status)
	}
}

func TestListsAndDetails(t *testing.T) {
	e := setup(t)
	var movies []Summary
	e.json("GET", "/api/movies", nil, 200, &movies)
	if len(movies) != 3 || movies[0].Title != "Alpha" || movies[1].Title != "Știința Ușoară" || movies[2].Title != "Zaza" {
		t.Fatalf("movies by title = %+v", movies)
	}
	e.json("GET", "/api/movies?sort=year", nil, 200, &movies)
	if movies[0].Year != 2018 || movies[2].Year != 1923 {
		t.Errorf("movies by year = %+v", movies)
	}
	if movies[0].PosterURL != nil {
		t.Errorf("poster url should be null until images exist")
	}

	var shows []ShowSummary
	e.json("GET", "/api/shows", nil, 200, &shows)
	if len(shows) != 1 || shows[0].Title != "My Show" || shows[0].EpisodeCount != 4 || shows[0].SeasonCount != 2 || shows[0].UnwatchedCount != 4 {
		t.Fatalf("shows = %+v", shows)
	}

	var show struct {
		Show    ShowSummary `json:"show"`
		Seasons []seasonOut `json:"seasons"`
		NextUp  *string     `json:"nextUpId"`
	}
	e.json("GET", "/api/shows/"+shows[0].ID, nil, 200, &show)
	if len(show.Seasons) != 3 || show.Seasons[0].Title != "Season 1" || show.Seasons[2].Title != "Specials" ||
		len(show.Seasons[0].Episodes) != 3 || show.Seasons[0].Episodes[1].Title != "Two" || *show.Seasons[0].Episodes[1].Episode != 2 {
		t.Fatalf("show = %+v", show)
	}
	if show.NextUp == nil || *show.NextUp != show.Seasons[0].Episodes[0].ID {
		t.Errorf("next up before watching = %v", show.NextUp)
	}

	var it itemResp
	e.json("GET", "/api/items/"+movies[0].ID, nil, 200, &it)
	if it.Item.Title != "Știința Ușoară" || it.FileName != "Știința Ușoară (2018).mkv" {
		t.Fatalf("item = %+v", it.Item)
	}
	var labels []string
	for _, s := range it.Subtitles {
		labels = append(labels, s.Label)
		if s.URL == nil || s.MIME != "application/x-subrip" || s.Embedded {
			t.Errorf("subtitle = %+v", s)
		}
	}
	if strings.Join(labels, ",") != "English (Forced),English,Romanian" {
		t.Errorf("labels = %v", labels)
	}

	// Episode details link to the next episode, across seasons but not into specials.
	ep3 := show.Seasons[0].Episodes[2]
	it = itemResp{}
	e.json("GET", "/api/items/"+ep3.ID, nil, 200, &it)
	if it.NextEpisode == nil || it.NextEpisode.Title != "Four" || it.Item.ShowTitle != "My Show" {
		t.Errorf("next episode of S01E03 = %+v", it.NextEpisode)
	}
	s2 := show.Seasons[1].Episodes[0]
	it = itemResp{}
	e.json("GET", "/api/items/"+s2.ID, nil, 200, &it)
	if it.NextEpisode != nil {
		t.Errorf("last regular episode continued into specials: %+v", it.NextEpisode)
	}
}

func TestSubtitleConversion(t *testing.T) {
	e := setup(t)
	var movies []Summary
	e.json("GET", "/api/movies?sort=year", nil, 200, &movies)
	var it itemResp
	e.json("GET", "/api/items/"+movies[0].ID, nil, 200, &it)
	var roURL string
	for _, s := range it.Subtitles {
		if s.Language == "ro" {
			roURL = *s.URL
		}
	}
	for i := 0; i < 2; i++ { // second request is served from the cache
		resp := e.do("GET", roURL, nil, nil)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/x-subrip; charset=utf-8" {
			t.Fatalf("subs status %d type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if want := "1\n00:00:01,000 --> 00:00:02,000\n„Știința e ușoară”\n"; string(body) != want {
			t.Errorf("converted subtitle = %q, want %q", body, want)
		}
	}
	resp := e.do("GET", strings.TrimSuffix(roURL, ".srt")+".vtt", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("wrong extension status = %d", resp.StatusCode)
	}
}

func TestDirectPlayRanges(t *testing.T) {
	e := setup(t)
	var movies []Summary
	e.json("GET", "/api/movies?sort=year", nil, 200, &movies)
	url := "/stream/" + movies[0].ID + "/file"

	resp := e.do("GET", url, nil, map[string]string{"Range": "bytes=0-999"})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || len(body) != 1000 || !bytes.Equal(body, e.movie[:1000]) {
		t.Fatalf("range 0-999: status %d, %d bytes", resp.StatusCode, len(body))
	}
	if resp.Header.Get("Content-Range") != "bytes 0-999/5000" || resp.Header.Get("Content-Type") != "video/x-matroska" {
		t.Errorf("headers = %v", resp.Header)
	}

	resp = e.do("GET", url, nil, map[string]string{"Range": "bytes=4900-"})
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || !bytes.Equal(body, e.movie[4900:]) {
		t.Errorf("open-ended range: %d, %d bytes", resp.StatusCode, len(body))
	}

	resp = e.do("HEAD", url, nil, nil)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.ContentLength != 5000 || resp.Header.Get("Accept-Ranges") != "bytes" {
		t.Errorf("HEAD: %d len %d", resp.StatusCode, resp.ContentLength)
	}

	// If-Range with a stale date returns the full file.
	resp = e.do("GET", url, nil, map[string]string{"Range": "bytes=0-9", "If-Range": "Mon, 02 Jan 2006 15:04:05 GMT"})
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(body) != 5000 {
		t.Errorf("stale If-Range: %d, %d bytes", resp.StatusCode, len(body))
	}

	resp = e.do("GET", url, nil, map[string]string{"Range": "bytes=6000-7000"})
	resp.Body.Close()
	if resp.StatusCode != 416 {
		t.Errorf("unsatisfiable range: %d", resp.StatusCode)
	}
}

func TestProgressHomeAndNextUp(t *testing.T) {
	e := setup(t)
	var shows []ShowSummary
	e.json("GET", "/api/shows", nil, 200, &shows)
	var show struct {
		Seasons []seasonOut `json:"seasons"`
	}
	e.json("GET", "/api/shows/"+shows[0].ID, nil, 200, &show)
	ep1, ep2 := show.Seasons[0].Episodes[0], show.Seasons[0].Episodes[1]

	var movies []Summary
	e.json("GET", "/api/movies?sort=year", nil, 200, &movies)
	movie := movies[0]

	// Under 2 minutes: not resumable.
	var p progressOut
	e.json("PUT", "/api/items/"+movie.ID+"/progress", map[string]any{"positionMs": 60000, "durationMs": 6000000, "paused": false}, 200, &p)
	if p.PositionMs != 0 || p.Watched {
		t.Errorf("early progress = %+v", p)
	}
	e.json("PUT", "/api/items/"+movie.ID+"/progress", map[string]any{"positionMs": 1800000, "durationMs": 6000000}, 200, &p)
	if p.PositionMs != 1800000 || p.LastPlayedAt == nil {
		t.Errorf("mid progress = %+v", p)
	}
	// Episode 1 watched to 95%.
	e.now = e.now.Add(time.Minute)
	e.json("PUT", "/api/items/"+ep1.ID+"/progress", map[string]any{"positionMs": 2850000, "durationMs": 3000000}, 200, &p)
	if !p.Watched || p.PlayCount != 1 || p.PositionMs != 0 {
		t.Errorf("episode finished = %+v", p)
	}

	var home struct {
		Hero                []json.RawMessage `json:"hero"`
		ContinueWatching    []Summary         `json:"continueWatching"`
		NextUp              []Summary         `json:"nextUp"`
		RecentlyAddedMovies []Summary         `json:"recentlyAddedMovies"`
		RecentlyAddedEps    []Summary         `json:"recentlyAddedEpisodes"`
		Movies              []Summary         `json:"movies"`
		Shows               []ShowSummary     `json:"shows"`
	}
	e.json("GET", "/api/home", nil, 200, &home)
	if len(home.ContinueWatching) != 1 || home.ContinueWatching[0].ID != movie.ID || home.ContinueWatching[0].PositionMs != 1800000 {
		t.Errorf("continue watching = %+v", home.ContinueWatching)
	}
	if len(home.NextUp) != 1 || home.NextUp[0].ID != ep2.ID {
		t.Errorf("next up = %+v", home.NextUp)
	}
	if len(home.Movies) != 3 || len(home.Shows) != 1 || len(home.Hero) == 0 || len(home.Hero) > 5 {
		t.Errorf("home rows: movies %d shows %d hero %d", len(home.Movies), len(home.Shows), len(home.Hero))
	}
	// Recently added excludes watched items and specials.
	for _, r := range home.RecentlyAddedEps {
		if r.ID == ep1.ID || (r.Season != nil && *r.Season == 0) {
			t.Errorf("recently added episodes contains %+v", r)
		}
	}
	if len(home.RecentlyAddedEps) != 3 || len(home.RecentlyAddedMovies) != 3 {
		t.Errorf("recently added: %d episodes, %d movies", len(home.RecentlyAddedEps), len(home.RecentlyAddedMovies))
	}

	// Mark the whole of season 1 watched -> next up moves to S02E01.
	var r map[string]any
	e.json("POST", "/api/shows/"+shows[0].ID+"/watched?season=1", nil, 200, &r)
	if r["updated"].(float64) != 3 {
		t.Errorf("season watched = %v", r)
	}
	e.now = e.now.Add(time.Minute)
	e.json("POST", "/api/items/"+ep2.ID+"/watched", nil, 200, &p) // touch last played
	e.json("GET", "/api/home", nil, 200, &home)
	if len(home.NextUp) != 1 || home.NextUp[0].Title != "Four" {
		t.Errorf("next up after season = %+v", home.NextUp)
	}
	e.json("DELETE", "/api/items/"+ep1.ID+"/watched", nil, 200, &p)
	if p.Watched {
		t.Errorf("unwatch = %+v", p)
	}
	e.json("DELETE", "/api/shows/"+shows[0].ID+"/watched", nil, 200, &r)
	e.json("POST", "/api/shows/"+shows[0].ID+"/watched?season=9", nil, 404, nil)

	// Shows not played for 60 days drop out of next up.
	e.json("POST", "/api/items/"+ep1.ID+"/watched", nil, 200, &p)
	e.now = e.now.Add(61 * 24 * time.Hour)
	e.json("GET", "/api/home", nil, 200, &home)
	if len(home.NextUp) != 0 {
		t.Errorf("stale next up = %+v", home.NextUp)
	}
}

func TestSearch(t *testing.T) {
	e := setup(t)
	type res struct {
		Movies   []Summary     `json:"movies"`
		Shows    []ShowSummary `json:"shows"`
		Episodes []Summary     `json:"episodes"`
	}
	var r res
	e.json("GET", "/api/search?q=stiinta", nil, 200, &r)
	if len(r.Movies) != 1 || r.Movies[0].Title != "Știința Ușoară" {
		t.Errorf("diacritics-insensitive search = %+v", r)
	}
	e.json("GET", "/api/search?q=%C8%99tiin%C8%9B", nil, 200, &r) // "științ"
	if len(r.Movies) != 1 {
		t.Errorf("search with diacritics = %+v", r)
	}
	e.json("GET", "/api/search?q=usoa", nil, 200, &r)
	if len(r.Movies) != 1 {
		t.Errorf("prefix on second word = %+v", r)
	}
	e.json("GET", "/api/search?q=tiinta", nil, 200, &r)
	if len(r.Movies) != 0 {
		t.Errorf("infix must not match = %+v", r)
	}
	e.json("GET", "/api/search?q=my", nil, 200, &r)
	if len(r.Shows) != 1 || len(r.Movies) != 0 {
		t.Errorf("show search = %+v", r)
	}
	e.json("GET", "/api/search?q=thr", nil, 200, &r)
	if len(r.Episodes) != 1 || r.Episodes[0].Title != "Three" {
		t.Errorf("episode search = %+v", r)
	}
	e.json("GET", "/api/search?q=", nil, 200, &r)
	if len(r.Movies)+len(r.Shows)+len(r.Episodes) != 0 {
		t.Errorf("empty query = %+v", r)
	}
}

func TestGzipJSON(t *testing.T) {
	e := setup(t)
	// Asked for: compressed, and decodes to the same JSON.
	resp := e.do("GET", "/api/movies", nil, map[string]string{"Accept-Encoding": "gzip"})
	defer resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "gzip" || !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("headers = %v", resp.Header)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var list []Summary
	if err := json.NewDecoder(zr).Decode(&list); err != nil || len(list) == 0 {
		t.Fatalf("decode: %v (%d items)", err, len(list))
	}
	// Errors are JSON too.
	resp2 := e.do("GET", "/api/nope", nil, map[string]string{"Accept-Encoding": "gzip"})
	defer resp2.Body.Close()
	zr2, err := gzip.NewReader(resp2.Body)
	if err != nil || resp2.StatusCode != 404 {
		t.Fatalf("404: %v %d", err, resp2.StatusCode)
	}
	var er map[string]string
	if json.NewDecoder(zr2).Decode(&er); er["error"] == "" {
		t.Errorf("error body = %v", er)
	}
	// Not asked for: plain.
	resp3 := e.do("GET", "/api/info", nil, map[string]string{"Accept-Encoding": "identity"})
	defer resp3.Body.Close()
	if resp3.Header.Get("Content-Encoding") != "" {
		t.Errorf("identity got %q", resp3.Header.Get("Content-Encoding"))
	}
}
