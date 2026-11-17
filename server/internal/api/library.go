package api

import (
	"context"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/acip/minimedia/server/internal/images"
	"github.com/acip/minimedia/server/internal/probe"
	"github.com/acip/minimedia/server/internal/progress"
	"github.com/acip/minimedia/server/internal/store"
	"github.com/acip/minimedia/server/internal/subs"
	"github.com/acip/minimedia/server/internal/textutil"
)

// Summary is the item shape used in rows (PLAN.md section 6).
type Summary struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"` // movie | episode
	Title        string  `json:"title"`
	Year         int     `json:"year,omitempty"`
	ShowID       string  `json:"showId,omitempty"`
	ShowTitle    string  `json:"showTitle,omitempty"`
	Season       *int    `json:"season,omitempty"`
	Episode      *int    `json:"episode,omitempty"`
	EpisodeEnd   *int    `json:"episodeEnd,omitempty"`
	EpisodeTitle string  `json:"episodeTitle,omitempty"`
	RuntimeMs    int64   `json:"runtimeMs"`
	PositionMs   int64   `json:"positionMs"`
	Watched      bool    `json:"watched"`
	PosterURL    *string `json:"posterUrl"`
	BackdropURL  *string `json:"backdropUrl"`
	LogoURL      *string `json:"logoUrl,omitempty"`
	StillURL     *string `json:"stillUrl,omitempty"`
	AddedAt      int64   `json:"addedAt"`
	// Only in the home hero (the banner shows them); rows stay small.
	Overview string   `json:"overview,omitempty"`
	Genres   []string `json:"genres,omitempty"`
}

// ShowSummary is a show in rows and grids.
type ShowSummary struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"` // show
	Title          string   `json:"title"`
	Year           int      `json:"year,omitempty"`
	Genres         []string `json:"genres"`
	SeasonCount    int      `json:"seasonCount"`
	EpisodeCount   int      `json:"episodeCount"`
	UnwatchedCount int      `json:"unwatchedCount"`
	PosterURL      *string  `json:"posterUrl"`
	BackdropURL    *string  `json:"backdropUrl"`
	LogoURL        *string  `json:"logoUrl,omitempty"`
	AddedAt        int64    `json:"addedAt"`
	Overview       string   `json:"overview,omitempty"` // home hero only
}

// artwork holds the best stored image per owner and kind, for building URLs.
type artwork map[string]map[string]store.Image

// artwork loads image references for items (and their shows) and shows.
func (s *Server) artwork(ctx context.Context, items []*store.Item, shows []*store.Show) artwork {
	var ids []string
	for _, it := range items {
		ids = append(ids, it.ID, it.ShowID)
	}
	for _, sh := range shows {
		ids = append(ids, sh.ID)
	}
	refs, err := s.store.ImageRefs(ctx, ids)
	if err != nil {
		s.log.Warn("load image refs", "err", err)
		return artwork{}
	}
	return refs
}

func (a artwork) url(owner, kind string) *string {
	if im, ok := a[owner][kind]; ok {
		u := images.URL(owner, kind, im.Version)
		return &u
	}
	return nil
}

// frameURL is the frame-grab fallback (backdrops and stills, PLAN.md 3.8).
func frameURL(owner, kind, version string) *string {
	u := images.URL(owner, kind, version)
	return &u
}

func summary(it *store.Item, art artwork) Summary {
	s := Summary{
		ID: it.ID, Kind: it.Kind, Title: it.Title, Year: it.Year,
		RuntimeMs: it.RuntimeMs, PositionMs: it.Progress.PositionMs, Watched: it.Progress.Watched,
		AddedAt: it.AddedAt,
	}
	probed := it.RuntimeMs > 0
	if it.Kind == "movie" {
		s.PosterURL, s.BackdropURL, s.LogoURL = art.url(it.ID, "poster"), art.url(it.ID, "backdrop"), art.url(it.ID, "logo")
		if s.BackdropURL == nil && probed {
			s.BackdropURL = frameURL(it.ID, "backdrop", images.FrameVersion(it.MtimeMs, it.Size))
		}
	}
	if it.Kind == "episode" {
		s.PosterURL, s.LogoURL = art.url(it.ShowID, "poster"), art.url(it.ShowID, "logo")
		if s.BackdropURL = art.url(it.ShowID, "backdrop"); s.BackdropURL == nil {
			s.BackdropURL = frameURL(it.ShowID, "backdrop", "f")
		}
		if s.StillURL = art.url(it.ID, "still"); s.StillURL == nil && probed {
			s.StillURL = frameURL(it.ID, "still", images.FrameVersion(it.MtimeMs, it.Size))
		}
		season, ep := it.Season, it.Episode
		s.ShowID, s.ShowTitle, s.Season, s.Episode = it.ShowID, it.ShowTitle, &season, &ep
		if it.EpisodeEnd > 0 {
			end := it.EpisodeEnd
			s.EpisodeEnd = &end
		}
		if s.Title == "" {
			s.Title = "Episode " + strconv.Itoa(it.Episode)
			if it.EpisodeEnd > 0 {
				s.Title += "–" + strconv.Itoa(it.EpisodeEnd)
			}
		}
		s.EpisodeTitle = s.Title
		s.Year = 0
	}
	return s
}

func summaries(items []*store.Item, art artwork) []Summary {
	out := make([]Summary, 0, len(items))
	for _, it := range items {
		out = append(out, summary(it, art))
	}
	return out
}

func showSummary(sh *store.Show, art artwork) ShowSummary {
	g := sh.Genres
	if g == nil {
		g = []string{}
	}
	added := sh.LastAddedAt
	if added == 0 {
		added = sh.AddedAt
	}
	ss := ShowSummary{
		ID: sh.ID, Kind: "show", Title: sh.Title, Year: sh.Year, Genres: g,
		SeasonCount: sh.SeasonCount, EpisodeCount: sh.EpisodeCount, UnwatchedCount: sh.EpisodeCount - sh.WatchedCount,
		AddedAt:   added,
		PosterURL: art.url(sh.ID, "poster"), BackdropURL: art.url(sh.ID, "backdrop"), LogoURL: art.url(sh.ID, "logo"),
	}
	if ss.BackdropURL == nil {
		ss.BackdropURL = frameURL(sh.ID, "backdrop", "f") // a frame from the first episode
	}
	return ss
}

func showSummaries(list []*store.Show, art artwork) []ShowSummary {
	out := make([]ShowSummary, 0, len(list))
	for _, sh := range list {
		out = append(out, showSummary(sh, art))
	}
	return out
}

func sortParam(r *http.Request) string {
	switch v := r.URL.Query().Get("sort"); v {
	case "title", "added", "year":
		return v
	}
	return "title"
}

func (s *Server) movies(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListMovies(r.Context(), sortParam(r), strings.TrimSpace(r.URL.Query().Get("genre")), 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := summaries(list, s.artwork(r.Context(), list, nil))
	for i, it := range list {
		out[i].Genres = it.Genres // the grid's genre filter lists them
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) shows(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListShows(r.Context(), sortParam(r), 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if g := strings.TrimSpace(r.URL.Query().Get("genre")); g != "" {
		var f []*store.Show
		for _, sh := range list {
			for _, sg := range sh.Genres {
				if strings.EqualFold(sg, g) {
					f = append(f, sh)
					break
				}
			}
		}
		list = f
	}
	writeJSON(w, http.StatusOK, showSummaries(list, s.artwork(r.Context(), nil, list)))
}

type seasonOut struct {
	Season   int       `json:"season"`
	Title    string    `json:"title"`
	Episodes []Summary `json:"episodes"`
}

func (s *Server) show(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sh, err := s.store.GetShow(ctx, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	eps, err := s.store.Episodes(ctx, sh.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	art := s.artwork(ctx, eps, []*store.Show{sh})
	var seasons []seasonOut
	for _, e := range eps {
		if len(seasons) == 0 || seasons[len(seasons)-1].Season != e.Season {
			title := "Season " + strconv.Itoa(e.Season)
			if e.Season == 0 {
				title = "Specials"
			}
			seasons = append(seasons, seasonOut{Season: e.Season, Title: title})
		}
		es := summary(e, art)
		es.Overview = e.Overview // the episode list shows it
		seasons[len(seasons)-1].Episodes = append(seasons[len(seasons)-1].Episodes, es)
	}
	if seasons == nil {
		seasons = []seasonOut{}
	}
	var nextUp *string
	if n := nextUpEpisode(eps); n != nil {
		nextUp = &n.ID
	}
	var rating any
	if sh.Rating > 0 {
		rating = sh.Rating
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"show":     showSummary(sh, art),
		"overview": sh.Overview,
		"rating":   rating,
		"seasons":  seasons,
		"nextUpId": nextUp, // the episode the app should focus by default
	})
}

// nextUpEpisode returns the first unwatched regular episode after the most
// recently watched one; with nothing watched yet, the first unwatched episode.
func nextUpEpisode(eps []*store.Item) *store.Item {
	var regular []*store.Item
	for _, e := range eps {
		if e.Season > 0 {
			regular = append(regular, e)
		}
	}
	base := -1
	var latest time.Time
	for i, e := range regular {
		if e.Progress.Watched && e.Progress.LastPlayedAt.After(latest) {
			base, latest = i, e.Progress.LastPlayedAt
		}
	}
	// A partially watched episode played after the last finished one is next.
	for i, e := range regular {
		if !e.Progress.Watched && e.Progress.PositionMs > 0 && e.Progress.LastPlayedAt.After(latest) {
			return regular[i]
		}
	}
	for _, e := range regular[base+1:] {
		if !e.Progress.Watched {
			return e
		}
	}
	return nil
}

// ---- item detail -------------------------------------------------------------

type audioOut struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec"`
	Channels int    `json:"channels"`
	Layout   string `json:"layout,omitempty"`
	Language string `json:"language"`
	Title    string `json:"title,omitempty"`
	Label    string `json:"label"`
	Default  bool   `json:"default"`
}

type subtitleOut struct {
	ID          string  `json:"id"`
	Language    string  `json:"language"`
	Label       string  `json:"label"`
	Forced      bool    `json:"forced"`
	SDH         bool    `json:"sdh"`
	Embedded    bool    `json:"embedded"`
	StreamIndex *int    `json:"streamIndex,omitempty"`
	Codec       string  `json:"codec"`
	URL         *string `json:"url"` // null for embedded tracks played from the container; set when served as a file
	MIME        string  `json:"mime"`
}

type markerOut struct {
	Kind    string `json:"kind"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
}

type progressOut struct {
	PositionMs   int64  `json:"positionMs"`
	DurationMs   int64  `json:"durationMs"`
	Watched      bool   `json:"watched"`
	PlayCount    int    `json:"playCount"`
	LastPlayedAt *int64 `json:"lastPlayedAt"`
}

func progressJSON(p progress.State) progressOut {
	o := progressOut{PositionMs: p.PositionMs, DurationMs: p.DurationMs, Watched: p.Watched, PlayCount: p.PlayCount}
	if !p.LastPlayedAt.IsZero() {
		ms := p.LastPlayedAt.UnixMilli()
		o.LastPlayedAt = &ms
	}
	return o
}

func (s *Server) item(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	it, err := s.store.GetItem(ctx, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var info *probe.Info
	if it.ProbeJSON != "" {
		info, _ = probe.Parse([]byte(it.ProbeJSON))
	}
	if info == nil {
		info = &probe.Info{}
	}

	audio := make([]audioOut, 0, len(info.Audio))
	for _, a := range info.Audio {
		audio = append(audio, audioOut{Index: a.Index, Codec: a.Codec, Channels: a.Channels, Layout: a.Layout,
			Language: a.Language, Title: a.Title, Label: probe.AudioLabel(a), Default: a.Default})
	}

	stored, err := s.store.Subtitles(ctx, it.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	subsOut := subtitleList(it.ID, stored)

	ms, err := s.store.Markers(ctx, it.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	markers := make([]markerOut, 0, len(ms))
	for _, m := range ms {
		markers = append(markers, markerOut{m.Kind, m.StartMs, m.EndMs})
	}
	var pm []probe.Marker
	for _, m := range ms {
		pm = append(pm, probe.Marker{Kind: m.Kind, StartMs: m.StartMs, EndMs: m.EndMs})
	}
	duration := it.RuntimeMs
	creditsAt, _ := probe.CreditsStartMs(pm, duration)

	var next *store.Item
	if it.Kind == "episode" {
		if eps, err := s.store.Episodes(ctx, it.ShowID); err == nil {
			for i, e := range eps {
				// Specials sort last; a regular episode never "continues" into them.
				if e.ID == it.ID && i+1 < len(eps) && (eps[i+1].Season > 0 || it.Season == 0) {
					next = eps[i+1]
				}
			}
		}
	}
	list := []*store.Item{it}
	if next != nil {
		list = append(list, next)
	}
	art := s.artwork(ctx, list, nil)

	resp := map[string]any{
		"item":           summary(it, art),
		"overview":       it.Overview,
		"genres":         nonNilStrings(it.Genres),
		"rating":         nil,
		"durationMs":     duration,
		"container":      info.Container,
		"fileName":       filepath.Base(it.Path),
		"size":           it.Size,
		"bitRate":        info.BitRate,
		"video":          info.Video,
		"audioTracks":    audio,
		"subtitles":      subsOut,
		"markers":        markers,
		"nextUpAtMs":     creditsAt, // when the "next episode" card should appear (PLAN.md 3.6)
		"progress":       progressJSON(it.Progress),
		"trickplayReady": s.trickplayInfo(it) != nil,
		"probed":         it.ProbeJSON != "",
	}
	if it.Rating > 0 {
		resp["rating"] = it.Rating
	}
	if next != nil {
		resp["nextEpisode"] = summary(next, art)
	}
	writeJSON(w, http.StatusOK, resp)
}

func subtitleList(itemID string, stored []store.Subtitle) []subtitleOut {
	tracks := make([]subs.Track, len(stored))
	for i, st := range stored {
		tracks[i] = subs.Track{Language: st.Language, Forced: st.Forced, SDH: st.SDH, Extra: st.Label}
	}
	labels := subs.Labels(tracks)
	out := make([]subtitleOut, 0, len(stored))
	for i, st := range stored {
		o := subtitleOut{ID: st.ID, Language: st.Language, Label: labels[i], Forced: st.Forced, SDH: st.SDH,
			Embedded: st.Source == "embedded", Codec: st.Codec, MIME: subs.MIME(st.Codec)}
		if o.Embedded {
			idx := st.StreamIndex
			o.StreamIndex = &idx
		} else {
			u := "/subs/" + itemID + "/" + st.ID + "." + st.Codec
			o.URL = &u
		}
		out = append(out, o)
	}
	return out
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---- home --------------------------------------------------------------------

const (
	recentWindow = 30 * 24 * time.Hour
	nextUpWindow = 60 * 24 * time.Hour
)

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := s.now()

	cont, err := s.store.ContinueWatching(ctx, 20)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	inContinue := map[string]bool{}
	for _, it := range cont {
		inContinue[it.ID] = true
	}

	var nextUp []*store.Item
	active, err := s.store.ActiveShows(ctx, now.Add(-nextUpWindow))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, showID := range active {
		eps, err := s.store.Episodes(ctx, showID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if n := nextUpEpisode(eps); n != nil && !inContinue[n.ID] {
			nextUp = append(nextUp, n)
		}
		if len(nextUp) >= 20 {
			break
		}
	}

	recentMovies, err := s.store.RecentlyAdded(ctx, "movie", now.Add(-recentWindow), 20)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	recentEps, err := s.store.RecentlyAdded(ctx, "episode", now.Add(-recentWindow), 20)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	latestMovies, err := s.store.ListMovies(ctx, "added", "", 30)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	latestShows, err := s.store.ListShows(ctx, "added", 30)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	var all []*store.Item
	for _, l := range [][]*store.Item{cont, nextUp, recentMovies, recentEps, latestMovies} {
		all = append(all, l...)
	}
	art := s.artwork(ctx, all, latestShows)

	// Hero: the newest movies and shows, mixed by date added. Titles with
	// TMDB/local artwork (a logo or a real backdrop) come first, since the hero
	// is a full-screen backdrop with the title logo.
	type heroItem struct {
		added   int64
		artwork bool
		v       any
	}
	var hero []heroItem
	for i, m := range latestMovies {
		if i >= 10 {
			break
		}
		_, hasBackdrop := art[m.ID]["backdrop"]
		hs := summary(m, art)
		hs.Overview, hs.Genres = m.Overview, m.Genres
		hero = append(hero, heroItem{m.AddedAt, hasBackdrop, hs})
	}
	for i, sh := range latestShows {
		if i >= 10 {
			break
		}
		ss := showSummary(sh, art)
		ss.Overview = sh.Overview
		_, hasBackdrop := art[sh.ID]["backdrop"]
		hero = append(hero, heroItem{ss.AddedAt, hasBackdrop, ss})
	}
	sort.SliceStable(hero, func(i, j int) bool {
		if hero[i].artwork != hero[j].artwork {
			return hero[i].artwork
		}
		return hero[i].added > hero[j].added
	})
	heroOut := []any{}
	for i, h := range hero {
		if i >= 5 {
			break
		}
		heroOut = append(heroOut, h.v)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"hero":                  heroOut,
		"continueWatching":      summaries(cont, art),
		"nextUp":                summaries(nextUp, art),
		"recentlyAddedMovies":   summaries(recentMovies, art),
		"recentlyAddedEpisodes": summaries(recentEps, art),
		"movies":                summaries(latestMovies, art),
		"shows":                 showSummaries(latestShows, art),
	})
}

// ---- search ------------------------------------------------------------------

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := map[string]any{"movies": []Summary{}, "shows": []ShowSummary{}, "episodes": []Summary{}}
	if q == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	ctx := r.Context()
	items, err := s.store.AllItems(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	showList, err := s.store.ListShows(ctx, "title", 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Both the display title (TMDB, maybe localised) and the file-name title match.
	matches := func(title, fileTitle string) bool {
		return textutil.MatchPrefixWords(q, title) || (fileTitle != title && textutil.MatchPrefixWords(q, fileTitle))
	}
	const max = 50
	var movies, episodes []*store.Item
	for _, it := range items {
		if !matches(it.Title, it.FileTitle) {
			continue
		}
		if it.Kind == "movie" && len(movies) < max {
			movies = append(movies, it)
		} else if it.Kind == "episode" && len(episodes) < max {
			episodes = append(episodes, it)
		}
	}
	var shows []*store.Show
	for _, sh := range showList {
		if matches(sh.Title, sh.FileTitle) && len(shows) < max {
			shows = append(shows, sh)
		}
	}
	art := s.artwork(ctx, append(append([]*store.Item{}, movies...), episodes...), shows)
	out["movies"], out["shows"], out["episodes"] = summaries(movies, art), showSummaries(shows, art), summaries(episodes, art)
	writeJSON(w, http.StatusOK, out)
}
