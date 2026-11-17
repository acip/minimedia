package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/acip/minimedia/server/internal/library"
	"github.com/acip/minimedia/server/internal/progress"
	"github.com/acip/minimedia/server/internal/trickplay"
)

// fakeProbe is ffprobe output for a 100-minute film with DTS, an AC3
// commentary and a Romanian AAC dub, plus an English forced subtitle stream.
const fakeProbe = `{"streams":[
 {"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"avg_frame_rate":"24/1","pix_fmt":"yuv420p"},
 {"index":1,"codec_type":"audio","codec_name":"dts","channels":6,"channel_layout":"5.1(side)","tags":{"language":"eng"},"disposition":{"default":1}},
 {"index":2,"codec_type":"audio","codec_name":"ac3","channels":2,"tags":{"language":"eng","title":"Director's Commentary"}},
 {"index":3,"codec_type":"audio","codec_name":"aac","channels":2,"tags":{"language":"ron"}}
],"format":{"format_name":"matroska,webm","duration":"6000.000"}}`

func (e *env) movieID() string {
	return library.ItemID(filepath.Join(e.movies, "Știința Ușoară (2018)", "Știința Ușoară (2018).mkv"))
}

// probeMovie stores fake probe data for the Știința Ușoară test movie.
func (e *env) probeMovie() string {
	ctx := context.Background()
	id := e.movieID()
	it, err := e.api.store.GetItem(ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	it.ProbeJSON, it.RuntimeMs = fakeProbe, 6_000_000
	tx, _ := e.api.store.Begin(ctx)
	if err := tx.UpsertItem(it); err != nil {
		e.t.Fatal(err)
	}
	tx.Commit()
	return id
}

type playbackResp struct {
	Mode        string `json:"mode"`
	URL         string `json:"url"`
	Container   string `json:"container"`
	AudioIndex  *int   `json:"audioIndex"`
	AudioTracks []struct {
		Index    int    `json:"index"`
		Label    string `json:"label"`
		Playable bool   `json:"playable"`
		Selected bool   `json:"selected"`
	} `json:"audioTracks"`
	Subtitles []struct {
		ID       string  `json:"id"`
		Language string  `json:"language"`
		Selected bool    `json:"selected"`
		URL      *string `json:"url"`
	} `json:"subtitles"`
	SelectedSubtitleID *string  `json:"selectedSubtitleId"`
	ResumeMs           int64    `json:"resumeMs"`
	DurationMs         int64    `json:"durationMs"`
	NextUpAtMs         int64    `json:"nextUpAtMs"`
	UnsupportedReason  *string  `json:"unsupportedReason"`
	Warnings           []string `json:"warnings"`
}

var caps = map[string]any{
	"videoCodecs":   []string{"h264", "hevc"},
	"hdr":           []string{},
	"audioDecoders": []string{"aac", "ac3", "eac3"},
	"maxChannels":   6,
}

func TestPlaybackDecision(t *testing.T) {
	e := setup(t)
	id := e.probeMovie()

	// Preferred languages are [ro]: the Romanian dub plays directly, no subtitles.
	var p playbackResp
	e.json("POST", "/api/items/"+id+"/playback", caps, 200, &p)
	if p.Mode != "direct" || p.URL != "/stream/"+id+"/file" || p.Container != "mkv" || p.AudioIndex == nil || *p.AudioIndex != 3 {
		t.Errorf("default decision = %+v", p)
	}
	if p.SelectedSubtitleID != nil || p.DurationMs != 6_000_000 || p.NextUpAtMs != 5_970_000 || p.UnsupportedReason != nil || len(p.Warnings) != 0 {
		t.Errorf("decision details = %+v", p)
	}
	playable := map[int]bool{}
	for _, a := range p.AudioTracks {
		playable[a.Index] = a.Playable
	}
	if playable[1] || !playable[2] || !playable[3] {
		t.Errorf("playable = %v", playable)
	}

	// Choosing the DTS track: conversion would be needed (Phase 4), so another
	// playable main track is used, with a warning.
	c := map[string]any{"audioTrack": 1}
	for k, v := range caps {
		c[k] = v
	}
	e.json("POST", "/api/items/"+id+"/playback", c, 200, &p)
	if p.Mode != "direct" || *p.AudioIndex != 3 || len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "DTS") {
		t.Errorf("dts fallback = %+v", p)
	}

	// An English-speaking device setup: no AAC dub preference → ... and an AV1-only device.
	c = map[string]any{"videoCodecs": []string{"av1"}, "audioDecoders": []string{"aac"}}
	e.json("POST", "/api/items/"+id+"/playback", c, 200, &p)
	if p.UnsupportedReason == nil || !strings.Contains(*p.UnsupportedReason, "H.264") {
		t.Errorf("unsupported video = %+v", p.UnsupportedReason)
	}

	// Resume position comes from progress.
	if _, err := e.api.store.UpdateProgress(context.Background(), id, func(s progress.State) progress.State {
		return progress.Apply(s, 600_000, 6_000_000, 0, e.now)
	}); err != nil {
		t.Fatal(err)
	}
	e.json("POST", "/api/items/"+id+"/playback", nil, 200, &p) // empty body = default capabilities
	if p.ResumeMs != 600_000 {
		t.Errorf("resumeMs = %d", p.ResumeMs)
	}

	// An unprobed file still gets a direct-play answer, with a warning.
	alpha := library.ItemID(filepath.Join(e.movies, "Alpha.2001.1080p.BluRay.x264.mp4"))
	e.json("POST", "/api/items/"+alpha+"/playback", caps, 200, &p)
	if p.Mode != "direct" || p.Container != "mp4" || len(p.Warnings) != 1 || p.AudioIndex != nil {
		t.Errorf("unprobed = %+v", p)
	}
	e.json("POST", "/api/items/nope/playback", caps, 404, nil)
	bad, _ := http.NewRequest("POST", e.srv.URL+"/api/items/"+id+"/playback", strings.NewReader("{bad"))
	r, err := http.DefaultClient.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Errorf("bad JSON: %d", r.StatusCode)
	}
}

func TestPlaybackSubtitlesAndShowPrefs(t *testing.T) {
	e := setup(t)
	id := e.probeMovie()
	// Romanian speaker, English audio chosen explicitly: Romanian subtitles on.
	c := map[string]any{"audioTrack": 2}
	for k, v := range caps {
		c[k] = v
	}
	var p playbackResp
	e.json("POST", "/api/items/"+id+"/playback", c, 200, &p)
	if p.SelectedSubtitleID == nil {
		t.Fatalf("no subtitle selected: %+v", p)
	}
	for _, s := range p.Subtitles {
		if s.Selected && (s.ID != *p.SelectedSubtitleID || s.Language != "ro" || s.URL == nil) {
			t.Errorf("selected subtitle = %+v", s)
		}
	}

	// Show prefs.
	var shows []ShowSummary
	e.json("GET", "/api/shows", nil, 200, &shows)
	showID := shows[0].ID
	var prefs map[string]any
	e.json("GET", "/api/shows/"+showID+"/prefs", nil, 200, &prefs)
	if prefs["saved"] != false || prefs["subtitlesMode"] != "off" {
		t.Errorf("default prefs = %v", prefs)
	}
	e.json("PUT", "/api/shows/"+showID+"/prefs", map[string]any{"audioLanguage": "English", "subtitleLanguage": "rum", "subtitlesMode": "on"}, 200, &prefs)
	if prefs["audioLanguage"] != "en" || prefs["subtitleLanguage"] != "ro" || prefs["saved"] != true {
		t.Errorf("saved prefs = %v", prefs)
	}
	e.json("GET", "/api/shows/"+showID+"/prefs", nil, 200, &prefs)
	if prefs["audioLanguage"] != "en" || prefs["subtitlesMode"] != "on" {
		t.Errorf("reloaded prefs = %v", prefs)
	}
	e.json("PUT", "/api/shows/"+showID+"/prefs", map[string]any{"subtitlesMode": "sometimes"}, 400, nil)
	e.json("PUT", "/api/shows/"+showID+"/prefs", map[string]any{"audioLanguage": "klingon"}, 400, nil)
	e.json("GET", "/api/shows/nope/prefs", nil, 404, nil)
}

func TestTrickplayEndpoints(t *testing.T) {
	e := setup(t)
	id := e.probeMovie()
	var p struct {
		Trickplay *struct {
			IndexURL string `json:"indexUrl"`
		} `json:"trickplay"`
	}
	e.json("POST", "/api/items/"+id+"/playback", caps, 200, &p)
	if p.Trickplay != nil {
		t.Fatalf("trickplay before generation: %+v", p.Trickplay)
	}
	e.json("GET", "/trickplay/"+id+"/index.json", nil, 404, nil)

	// Put finished thumbnails where the generator would.
	it, _ := e.api.store.GetItem(context.Background(), id)
	g := e.api.opt.Trickplay
	idx := trickplay.Index{IntervalMs: 10000, TileWidth: 320, TileHeight: 180, Columns: 5, Rows: 5, Count: 600, Sheets: 24, Version: g.Version(it)}
	data, _ := json.Marshal(idx)
	write(t, filepath.Join(e.dataDir, "trickplay", id, "index.json"), data)
	write(t, g.SheetPath(id, 0), jpegBytes(t, 1600, 900))

	e.json("POST", "/api/items/"+id+"/playback", caps, 200, &p)
	if p.Trickplay == nil || !strings.HasPrefix(p.Trickplay.IndexURL, "/trickplay/"+id+"/index.json?v=") {
		t.Fatalf("trickplay = %+v", p.Trickplay)
	}
	var got trickplay.Index
	e.json("GET", p.Trickplay.IndexURL, nil, 200, &got)
	if got.Count != 600 || got.Sheets != 24 {
		t.Errorf("index = %+v", got)
	}
	resp := e.do("GET", "/trickplay/"+id+"/0.jpg", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" || !strings.Contains(resp.Header.Get("Cache-Control"), "max-age") {
		t.Errorf("sheet: %d %v", resp.StatusCode, resp.Header)
	}
	e.json("GET", "/trickplay/"+id+"/24.jpg", nil, 404, nil)
	e.json("GET", "/trickplay/"+id+"/x.png", nil, 404, nil)

	// The file changes: the thumbnails are stale until regenerated.
	it.MtimeMs++
	tx, _ := e.api.store.Begin(context.Background())
	tx.UpsertItem(it)
	tx.Commit()
	e.json("GET", "/trickplay/"+id+"/index.json", nil, 404, nil)
	var detail map[string]any
	e.json("GET", "/api/items/"+id, nil, 200, &detail)
	if detail["trickplayReady"] != false {
		t.Errorf("stale thumbnails reported ready")
	}
}

func jpegBytes(t *testing.T, w, h int) []byte {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestImageURLsAndEndpoint(t *testing.T) {
	e := setup(t)
	write(t, filepath.Join(e.movies, "Știința Ușoară (2018)", "poster.jpg"), jpegBytes(t, 600, 900))
	write(t, filepath.Join(e.shows, "My Show (2019)", "folder.jpg"), jpegBytes(t, 600, 900))
	if _, err := e.api.scanner.ScanAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.probeMovie()

	var movies []Summary
	e.json("GET", "/api/movies", nil, 200, &movies)
	var m *Summary
	for i := range movies {
		if movies[i].ID == e.movieID() {
			m = &movies[i]
		}
	}
	if m == nil || m.PosterURL == nil || !strings.HasPrefix(*m.PosterURL, "/images/"+m.ID+"/poster?v=") {
		t.Fatalf("movie summary = %+v", m)
	}
	if m.BackdropURL == nil || !strings.Contains(*m.BackdropURL, "/backdrop?v=f") {
		t.Errorf("probed movie without a backdrop gets a frame-grab URL: %v", m.BackdropURL)
	}
	for _, other := range movies {
		if other.ID != m.ID && (other.PosterURL != nil || other.BackdropURL != nil) {
			t.Errorf("unprobed movie without art has URLs: %+v", other)
		}
	}

	resp := e.do("GET", *m.PosterURL+"&w=300", nil, nil) // snaps up to 342
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("image: %d %s", resp.StatusCode, body)
	}
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(body)); err != nil || cfg.Width != 342 {
		t.Errorf("width = %d (%v)", cfg.Width, err)
	}
	etag := resp.Header.Get("ETag")
	resp = e.do("GET", *m.PosterURL+"&w=342", nil, map[string]string{"If-None-Match": etag})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match: %d", resp.StatusCode)
	}
	e.json("GET", "/images/"+m.ID+"/poster?w=abc", nil, 400, nil)
	e.json("GET", "/images/"+m.ID+"/banner", nil, 404, nil)
	e.json("GET", "/images/"+m.ID+"/logo", nil, 404, nil)

	// Shows and their episodes carry the show's artwork.
	var show struct {
		Show    ShowSummary `json:"show"`
		Seasons []struct {
			Episodes []Summary `json:"episodes"`
		} `json:"seasons"`
	}
	var shows []ShowSummary
	e.json("GET", "/api/shows", nil, 200, &shows)
	e.json("GET", "/api/shows/"+shows[0].ID, nil, 200, &show)
	if show.Show.PosterURL == nil || show.Show.BackdropURL == nil {
		t.Errorf("show art = %+v", show.Show)
	}
	ep := show.Seasons[0].Episodes[0]
	if ep.PosterURL == nil || *ep.PosterURL != *show.Show.PosterURL || ep.StillURL != nil {
		t.Errorf("episode art = poster %v still %v", ep.PosterURL, ep.StillURL)
	}
}
