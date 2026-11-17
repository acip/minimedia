package metadata

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/acip/minimedia/server/internal/store"
	"github.com/acip/minimedia/server/internal/textutil"
)

// retryAfter is how long an unmatched movie/show waits before another search.
const retryAfter = 7 * 24 * time.Hour

// Status is reported by GET /api/library/status.
type Status struct {
	Enabled   bool       `json:"enabled"`
	Running   bool       `json:"running"`
	LastRunAt *time.Time `json:"lastRunAt"`
	LastError string     `json:"lastError,omitempty"`
	store.MetaCounts
}

// Fetcher looks up movies, shows and episodes that need metadata.
type Fetcher struct {
	store  *store.Store
	client *Client
	log    *slog.Logger
	now    func() time.Time

	mu     sync.Mutex
	status Status
}

func NewFetcher(st *store.Store, c *Client, log *slog.Logger) *Fetcher {
	return &Fetcher{store: st, client: c, log: log, now: time.Now, status: Status{Enabled: true}}
}

func (f *Fetcher) Status(ctx context.Context) Status {
	f.mu.Lock()
	st := f.status
	f.mu.Unlock()
	if c, err := f.store.MetaCounts(ctx); err == nil {
		st.MetaCounts = c
	}
	return st
}

// RunOnce processes everything that needs metadata. It stops at the first
// network or authentication error (items stay pending and are retried after
// the next scan).
func (f *Fetcher) RunOnce(ctx context.Context) error {
	f.mu.Lock()
	f.status.Running = true
	f.mu.Unlock()
	err := f.run(ctx)
	now := f.now()
	f.mu.Lock()
	f.status.Running = false
	f.status.LastRunAt = &now
	f.status.LastError = ""
	if err != nil && ctx.Err() == nil {
		f.status.LastError = err.Error()
	}
	f.mu.Unlock()
	if err != nil && ctx.Err() == nil {
		f.log.Warn("metadata fetch stopped; will retry after the next scan", "err", err)
	}
	return err
}

func (f *Fetcher) run(ctx context.Context) error {
	movies, err := f.store.MoviesNeedingMeta(ctx, f.now().Add(-retryAfter))
	if err != nil {
		return err
	}
	shows, err := f.store.ShowsNeedingMeta(ctx, f.now().Add(-retryAfter))
	if err != nil {
		return err
	}
	if len(movies)+len(shows) == 0 {
		return nil
	}
	f.log.Info("fetching metadata", "movies", len(movies), "shows", len(shows))
	matched := 0
	for _, m := range movies {
		ok, err := f.movie(ctx, m)
		if err != nil {
			return err
		}
		if ok {
			matched++
		}
	}
	for _, sh := range shows {
		ok, err := f.show(ctx, sh)
		if err != nil {
			return err
		}
		if ok {
			matched++
		}
	}
	f.log.Info("metadata fetched", "matched", matched, "of", len(movies)+len(shows))
	return nil
}

// langCodes returns "ro-RO" and "ro" for image language filters.
func (f *Fetcher) langCodes() (full, short string) {
	full = f.client.Lang
	short, _, _ = strings.Cut(full, "-")
	return full, strings.ToLower(short)
}

func (f *Fetcher) english() bool {
	_, short := f.langCodes()
	return short == "en"
}

// ---- movies ------------------------------------------------------------------

func (f *Fetcher) movie(ctx context.Context, t store.MetaTarget) (bool, error) {
	id := t.Hint
	if id == 0 {
		var err error
		if id, err = f.search(ctx, "/search/movie", "year", t.Title, t.Year); err != nil {
			return false, err
		}
	}
	if id == 0 {
		f.log.Debug("no TMDB match", "movie", t.Title, "year", t.Year)
		return false, f.store.SetItemMeta(ctx, t.ID, nil, f.now())
	}
	d, err := f.details(ctx, "/movie/"+strconv.FormatInt(id, 10))
	if errors.Is(err, ErrNotFound) {
		f.log.Warn("TMDB id not found", "movie", t.Path, "id", id)
		return false, f.store.SetItemMeta(ctx, t.ID, nil, f.now())
	}
	if err != nil {
		return false, err
	}
	m := &store.Meta{TMDBID: d.ID, Title: d.Title, SortKey: textutil.SortKey(d.Title), Year: yearOf(d.ReleaseDate),
		Overview: d.Overview, Genres: genreNames(d.Genres), Rating: rating(d)}
	if err := f.store.SetItemMeta(ctx, t.ID, m, f.now()); err != nil {
		return false, err
	}
	return true, f.store.ReplaceImages(ctx, t.ID, "tmdb", f.artwork(d))
}

// ---- shows -------------------------------------------------------------------

func (f *Fetcher) show(ctx context.Context, t store.MetaTarget) (bool, error) {
	full := !t.Attempts || t.TMDBID == 0
	id := t.TMDBID
	if full {
		id = t.Hint
		if id == 0 {
			var err error
			if id, err = f.search(ctx, "/search/tv", "first_air_date_year", t.Title, t.Year); err != nil {
				return false, err
			}
		}
		if id == 0 {
			f.log.Debug("no TMDB match", "show", t.Title, "year", t.Year)
			return false, f.store.SetShowMeta(ctx, t.ID, nil, f.now())
		}
		d, err := f.details(ctx, "/tv/"+strconv.FormatInt(id, 10))
		if errors.Is(err, ErrNotFound) {
			f.log.Warn("TMDB id not found", "show", t.Path, "id", id)
			return false, f.store.SetShowMeta(ctx, t.ID, nil, f.now())
		}
		if err != nil {
			return false, err
		}
		m := &store.Meta{TMDBID: d.ID, Title: d.Name, SortKey: textutil.SortKey(d.Name), Year: yearOf(d.FirstAirDate),
			Overview: d.Overview, Genres: genreNames(d.Genres), Rating: rating(d)}
		if err := f.store.SetShowMeta(ctx, t.ID, m, f.now()); err != nil {
			return false, err
		}
		if err := f.store.ReplaceImages(ctx, t.ID, "tmdb", f.artwork(d)); err != nil {
			return false, err
		}
	}
	return true, f.episodes(ctx, t.ID, id, full)
}

// episodes fills in episode titles, overviews and stills, a season at a time.
func (f *Fetcher) episodes(ctx context.Context, showID string, tmdbID int64, all bool) error {
	refs, err := f.store.EpisodeRefs(ctx, showID)
	if err != nil {
		return err
	}
	bySeason := map[int][]store.EpisodeRef{}
	for _, r := range refs {
		if all || !r.Looked {
			bySeason[r.Season] = append(bySeason[r.Season], r)
		}
	}
	seasons := make([]int, 0, len(bySeason))
	for n := range bySeason {
		seasons = append(seasons, n)
	}
	sort.Ints(seasons)
	for _, n := range seasons {
		want := bySeason[n]
		eps, err := f.season(ctx, tmdbID, n, want)
		if err != nil {
			return err
		}
		for _, r := range want {
			m, still := episodeMeta(r, eps)
			if err := f.store.SetItemMeta(ctx, r.ID, m, f.now()); err != nil {
				return err
			}
			var ims []store.Image
			if still != "" {
				ims = []store.Image{{Kind: "still", Path: still, Version: version(still)}}
			}
			if err := f.store.ReplaceImages(ctx, r.ID, "tmdb", ims); err != nil {
				return err
			}
		}
	}
	return nil
}

// season fetches a season's episodes. A cached response missing a wanted
// episode (newly aired) is refetched; missing overviews fall back to English.
func (f *Fetcher) season(ctx context.Context, tmdbID int64, n int, want []store.EpisodeRef) (map[int]seasonEpisode, error) {
	p := "/tv/" + strconv.FormatInt(tmdbID, 10) + "/season/" + strconv.Itoa(n)
	full, _ := f.langCodes()
	fetch := func(lang string, fresh bool) (map[int]seasonEpisode, error) {
		var s season
		if err := f.client.get(ctx, p, url.Values{"language": {lang}}, fresh, &s); err != nil {
			return nil, err
		}
		out := map[int]seasonEpisode{}
		for _, e := range s.Episodes {
			out[e.EpisodeNumber] = e
		}
		return out, nil
	}
	eps, err := fetch(full, false)
	if errors.Is(err, ErrNotFound) {
		return map[int]seasonEpisode{}, nil // season unknown to TMDB (e.g. a fan-made "season")
	}
	if err != nil {
		return nil, err
	}
	for _, r := range want {
		if _, ok := eps[r.Episode]; !ok {
			if eps, err = fetch(full, true); err != nil && !errors.Is(err, ErrNotFound) {
				return nil, err
			}
			break
		}
	}
	if !f.english() {
		missing := false
		for _, r := range want {
			if e, ok := eps[r.Episode]; ok && e.Overview == "" {
				missing = true
			}
		}
		if missing {
			if en, err := fetch("en-US", false); err == nil {
				for k, e := range eps {
					if e.Overview == "" {
						e.Overview = en[k].Overview
						eps[k] = e
					}
				}
			}
		}
	}
	return eps, nil
}

// episodeMeta builds an episode's metadata; multi-episode files join the
// titles ("Part 1 / Part 2").
func episodeMeta(r store.EpisodeRef, eps map[int]seasonEpisode) (*store.Meta, string) {
	first, ok := eps[r.Episode]
	if !ok {
		return nil, ""
	}
	var titles []string
	if !genericEpisodeName(first.Name, r.Episode) {
		titles = append(titles, first.Name)
	}
	for n := r.Episode + 1; n <= r.EpisodeEnd; n++ {
		if e, ok := eps[n]; ok && !genericEpisodeName(e.Name, n) {
			titles = append(titles, e.Name)
		}
	}
	title := strings.Join(titles, " / ")
	m := &store.Meta{TMDBID: first.ID, Title: title, SortKey: textutil.SortKey(title), Overview: first.Overview}
	if first.VoteAverage > 0 {
		m.Rating = round1(first.VoteAverage)
	}
	return m, first.StillPath
}

// genericEpisodeName spots placeholder names like "Episode 5" / "Episodul 5",
// which are worse than a title from the file name.
func genericEpisodeName(name string, n int) bool {
	w := strings.Fields(name)
	return len(w) == 0 || (len(w) == 2 && w[1] == strconv.Itoa(n))
}

// ---- shared ------------------------------------------------------------------

// search returns the best TMDB match: exact title and year first, then
// TMDB's own ranking. Without a year match it retries without the year.
func (f *Fetcher) search(ctx context.Context, p, yearParam, title string, year int) (int64, error) {
	if strings.TrimSpace(title) == "" {
		return 0, nil
	}
	full, _ := f.langCodes()
	q := url.Values{"query": {title}, "language": {full}, "include_adult": {"false"}}
	if year > 0 {
		q.Set(yearParam, strconv.Itoa(year))
	}
	var r searchResponse
	if err := f.client.get(ctx, p, q, false, &r); err != nil {
		return 0, err
	}
	if len(r.Results) == 0 && year > 0 {
		q.Del(yearParam)
		if err := f.client.get(ctx, p, q, false, &r); err != nil {
			return 0, err
		}
	}
	return bestMatch(r.Results, title, year), nil
}

func bestMatch(results []searchResult, title string, year int) int64 {
	want := textutil.Fold(title)
	best, bestScore := int64(0), -1
	for i, r := range results {
		if i >= 10 {
			break
		}
		score := 0
		for _, t := range []string{r.Title, r.OriginalTitle, r.Name, r.OriginalName} {
			if t != "" && textutil.Fold(t) == want {
				score += 2
				break
			}
		}
		ry := yearOf(r.ReleaseDate + r.FirstAirDate)
		switch {
		case year == 0 || ry == 0:
			// nothing to compare
		case ry == year:
			score += 2
		case ry == year-1 || ry == year+1:
			score++
		default:
			score -= 3 // a different year is a different film (remakes), even with the same title
		}
		if score > bestScore {
			best, bestScore = r.ID, score
		}
	}
	if year > 0 && bestScore < 0 {
		return 0
	}
	return best
}

func (f *Fetcher) details(ctx context.Context, p string) (*details, error) {
	full, short := f.langCodes()
	q := url.Values{"language": {full}, "append_to_response": {"images"},
		"include_image_language": {short + ",en,null"}}
	var d details
	if err := f.client.get(ctx, p, q, false, &d); err != nil {
		return nil, err
	}
	if d.Overview == "" && !f.english() {
		var en details
		if err := f.client.get(ctx, p, url.Values{"language": {"en-US"}}, false, &en); err == nil {
			d.Overview = en.Overview
		}
	}
	return &d, nil
}

// artwork picks poster and backdrop (TMDB already localises them through the
// language parameter) and the best logo: metadata language, then English,
// then language-neutral; highest rated within each.
func (f *Fetcher) artwork(d *details) []store.Image {
	var out []store.Image
	add := func(kind, p string) {
		if p != "" {
			out = append(out, store.Image{Kind: kind, Path: p, Version: version(p)})
		}
	}
	add("poster", d.PosterPath)
	add("backdrop", d.BackdropPath)
	_, short := f.langCodes()
	rank := func(im imageInfo) int {
		switch {
		case im.Lang != nil && *im.Lang == short:
			return 0
		case im.Lang != nil && *im.Lang == "en":
			return 1
		case im.Lang == nil:
			return 2
		}
		return 3
	}
	var logo *imageInfo
	for i := range d.Images.Logos {
		l := &d.Images.Logos[i]
		if rank(*l) == 3 || strings.HasSuffix(strings.ToLower(l.FilePath), ".svg") {
			continue
		}
		if logo == nil || rank(*l) < rank(*logo) || (rank(*l) == rank(*logo) && l.VoteAverage > logo.VoteAverage) {
			logo = l
		}
	}
	if logo != nil {
		add("logo", logo.FilePath)
	}
	return out
}

func genreNames(gs []genre) []string {
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		if g.Name != "" {
			out = append(out, g.Name)
		}
	}
	return out
}

func rating(d *details) float64 {
	if d.VoteCount == 0 {
		return 0
	}
	return round1(d.VoteAverage)
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

func version(p string) string {
	sum := sha1.Sum([]byte(p))
	return "t" + hex.EncodeToString(sum[:])[:7]
}
