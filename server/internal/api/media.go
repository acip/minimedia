package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/acip/minimedia/server/internal/hls"
	"github.com/acip/minimedia/server/internal/images"
	"github.com/acip/minimedia/server/internal/playback"
	"github.com/acip/minimedia/server/internal/probe"
	"github.com/acip/minimedia/server/internal/progress"
	"github.com/acip/minimedia/server/internal/store"
	"github.com/acip/minimedia/server/internal/subs"
)

// ---- images ------------------------------------------------------------------

// GET /images/{ownerId}/{kind}?w=342|780|1280|1920 (PLAN.md 3.8). Other widths
// snap up to the next served size; no w = original size (at most 1920).
func (s *Server) image(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if !images.Kinds[kind] || s.images == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	width := 0
	if v := r.URL.Query().Get("w"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "w must be a positive number")
			return
		}
		width = images.Widths[len(images.Widths)-1]
		for _, a := range images.Widths {
			if a >= n {
				width = a
				break
			}
		}
	}
	err := s.images.Serve(w, r, r.PathValue("ownerId"), kind, width)
	switch {
	case err == nil:
	case errors.Is(err, images.ErrNoImage):
		writeError(w, http.StatusNotFound, "no "+kind+" image")
	case r.Context().Err() != nil:
	default:
		s.log.Warn("image failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusNotFound, "image unavailable")
	}
}

// ---- trickplay ---------------------------------------------------------------

type trickplayOut struct {
	IndexURL string `json:"indexUrl"`
	Version  string `json:"version"`
}

// trickplayInfo is the playback response's trickplay field: null until the
// thumbnails of the file in its current state are ready.
func (s *Server) trickplayInfo(it *store.Item) *trickplayOut {
	if s.opt.Trickplay == nil {
		return nil
	}
	idx, ok := s.opt.Trickplay.Index(it)
	if !ok {
		return nil
	}
	return &trickplayOut{IndexURL: "/trickplay/" + it.ID + "/index.json?v=" + idx.Version, Version: idx.Version}
}

func (s *Server) trickplayStatus() any {
	if s.opt.Trickplay == nil {
		return nil
	}
	return s.opt.Trickplay.Status()
}

// GET /trickplay/{id}/index.json and /trickplay/{id}/{n}.jpg (PLAN.md 5.2).
// 404 until ready. URLs carry ?v=<version>, so they can be cached for long.
func (s *Server) trickplay(w http.ResponseWriter, r *http.Request) {
	if s.opt.Trickplay == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	it, err := s.store.GetItem(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	idx, ok := s.opt.Trickplay.Index(it)
	if !ok {
		writeError(w, http.StatusNotFound, "thumbnails are not ready yet")
		return
	}
	file := r.PathValue("file")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Header().Set("ETag", `"`+idx.Version+"-"+file+`"`)
	if file == "index.json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(idx)
		return
	}
	n, err := strconv.Atoi(strings.TrimSuffix(file, ".jpg"))
	if err != nil || !strings.HasSuffix(file, ".jpg") || n < 0 || n >= idx.Sheets {
		writeError(w, http.StatusNotFound, "no such sheet")
		return
	}
	f, err := os.Open(s.opt.Trickplay.SheetPath(it.ID, n))
	if err != nil {
		writeError(w, http.StatusNotFound, "no such sheet")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// ---- show preferences --------------------------------------------------------

func prefsJSON(p store.ShowPrefs) map[string]any {
	return map[string]any{"audioLanguage": p.AudioLanguage, "subtitleLanguage": p.SubtitleLanguage,
		"subtitlesMode": p.SubtitlesMode, "saved": p.Set}
}

func (s *Server) getPrefs(w http.ResponseWriter, r *http.Request) {
	sh, err := s.store.GetShow(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.store.ShowPrefs(r.Context(), sh.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, prefsJSON(p))
}

func (s *Server) putPrefs(w http.ResponseWriter, r *http.Request) {
	sh, err := s.store.GetShow(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in store.ShowPrefs
	if err := decodeBody(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch in.SubtitlesMode {
	case "off", "on", "forced_only":
	case "":
		in.SubtitlesMode = "off"
	default:
		writeError(w, http.StatusBadRequest, "subtitlesMode must be off, on or forced_only")
		return
	}
	for _, l := range []*string{&in.AudioLanguage, &in.SubtitleLanguage} {
		if *l == "" {
			continue
		}
		code, ok := subs.LookupLang(*l)
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown language "+strconv.Quote(*l))
			return
		}
		*l = code
	}
	if err := s.store.SetShowPrefs(r.Context(), sh.ID, in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.Set = true
	writeJSON(w, http.StatusOK, prefsJSON(in))
}

// ---- playback decision -------------------------------------------------------

type playbackAudio struct {
	audioOut
	Playable bool `json:"playable"`
	Selected bool `json:"selected"`
}

type playbackSub struct {
	subtitleOut
	Selected  bool `json:"selected"`
	Available bool `json:"available"` // false: can't be shown in this playback mode
}

// POST /api/items/{id}/playback (PLAN.md 4.1): direct play, or HLS with the
// audio converted (4.3). Without a converter (tests, or a file that can't be
// converted) a file that needs it plays direct with another playable audio
// track if there is one, and the response carries a warning.
func (s *Server) playback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var caps playback.Caps
	if err := decodeBody(r, &caps); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	it, err := s.store.GetItem(ctx, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Debug("playback request", "item", it.ID, "video", caps.VideoCodecs, "hdr", caps.HDR,
		"audioDecoders", caps.AudioDecoders, "passthrough", caps.AudioPassthrough, "maxChannels", caps.MaxChannels,
		"audioTrack", caps.AudioTrack, "forceConvert", caps.ForceConvert, "client", r.RemoteAddr)
	var info *probe.Info
	var warnings []string
	if it.ProbeJSON != "" {
		info, _ = probe.Parse([]byte(it.ProbeJSON))
	}
	if info == nil {
		info = &probe.Info{}
		warnings = append(warnings, "This file hasn't been analysed (ffprobe failed or the scan is still running); trying direct play")
	}

	var prefs playback.Prefs
	if it.Kind == "episode" {
		p, err := s.store.ShowPrefs(ctx, it.ShowID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		prefs = playback.Prefs{AudioLanguage: p.AudioLanguage, SubtitleLanguage: p.SubtitleLanguage, SubtitlesMode: p.SubtitlesMode, Set: p.Set}
	}
	stored, err := s.store.Subtitles(ctx, it.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	subsIn := make([]playback.Sub, len(stored))
	for i, st := range stored {
		subsIn[i] = playback.Sub{ID: st.ID, Language: st.Language, Forced: st.Forced, SDH: st.SDH}
	}

	in := playback.Input{Info: info, Subs: subsIn, Caps: caps, Prefs: prefs,
		Preferred: s.opt.PreferredLanguages, AudioConvert: s.opt.AudioConvert}
	d := playback.Decide(in)
	warnings = append(warnings, d.Warnings...)
	if d.Mode == "hls" {
		src, err := hls.SourceFor(it.ID, it.Path, it.Size, it.MtimeMs, info, d.AudioIndex, d.ConvertTo)
		if s.opt.HLS == nil || err != nil {
			if err != nil {
				s.log.Warn("audio conversion not possible", "path", it.Path, "err", err)
			}
			d, warnings = directFallback(d, info, warnings, caps.ForceConvert)
		}
		// Re-run the subtitle choice for the audio track actually used and,
		// in HLS mode, among the subtitles that can travel with it.
		idx := d.AudioIndex
		in.Caps.AudioTrack = &idx
		in.AudioConvert = "never"
		if d.Mode == "hls" {
			var avail []playback.Sub
			for i, st := range stored {
				if _, text := textSubExt(st.Codec); st.Source == "external" || text {
					avail = append(avail, subsIn[i])
				}
			}
			in.Subs = avail
			// Start the work the player is about to ask for: the keyframe
			// index (for the playlist), then embedded subtitles. Both read the
			// whole file, so one after the other rather than competing.
			go func() {
				bg := context.Background()
				if _, err := s.opt.HLS.Keyframes(bg, src); err == nil && hasEmbeddedText(stored) {
					_ = s.extractSubtitles(bg, it, stored)
				}
			}()
		}
		d.SubtitleID = playback.Decide(in).SubtitleID
	}

	audio := make([]playbackAudio, 0, len(info.Audio))
	for _, a := range info.Audio {
		audio = append(audio, playbackAudio{
			audioOut: audioOut{Index: a.Index, Codec: a.Codec, Channels: a.Channels, Layout: a.Layout,
				Language: a.Language, Title: a.Title, Label: probe.AudioLabel(a), Default: a.Default},
			Playable: d.Playable[a.Index], Selected: a.Index == d.AudioIndex,
		})
	}
	var subsOut []playbackSub
	var selectedSub *string
	for i, so := range subtitleList(it.ID, stored) {
		ps := playbackSub{subtitleOut: so, Selected: so.ID == d.SubtitleID, Available: true}
		if d.Mode == "hls" && so.Embedded {
			// HLS segments carry no subtitles: text tracks are served as
			// extracted files (a url on an embedded track), image tracks
			// can't be shown (PLAN.md 4.1).
			if ext, ok := textSubExt(stored[i].Codec); ok {
				u := "/subs/" + it.ID + "/" + so.ID + "." + ext
				ps.URL, ps.MIME = &u, subs.MIME(ext)
			} else {
				ps.Available, ps.Selected = false, false
				ps.Label += " (unavailable with converted audio)"
			}
		}
		if ps.Selected {
			id := so.ID
			selectedSub = &id
		}
		subsOut = append(subsOut, ps)
	}
	if subsOut == nil {
		subsOut = []playbackSub{}
	}

	ms, err := s.store.Markers(ctx, it.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	markers := make([]markerOut, 0, len(ms))
	pm := make([]probe.Marker, 0, len(ms))
	for _, m := range ms {
		markers = append(markers, markerOut{m.Kind, m.StartMs, m.EndMs})
		pm = append(pm, probe.Marker{Kind: m.Kind, StartMs: m.StartMs, EndMs: m.EndMs})
	}
	duration := it.RuntimeMs
	creditsAt, _ := probe.CreditsStartMs(pm, duration)
	var resume int64
	if p := it.Progress; !p.Watched && progress.IsResumable(p.PositionMs, max(duration, p.DurationMs)) {
		resume = p.PositionMs
	}
	var audioIndex *int
	if d.AudioIndex >= 0 {
		audioIndex = &d.AudioIndex
	}
	var unsupported *string
	if d.UnsupportedReason != "" {
		unsupported = &d.UnsupportedReason
	}
	if warnings == nil {
		warnings = []string{}
	}
	url, container := "/stream/"+it.ID+"/file", containerOf(it.Path)
	if d.Mode == "hls" {
		url, container = hlsURL(it.ID, d.AudioIndex, d.ConvertTo), "ts"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":               d.Mode,
		"url":                url,
		"container":          container,
		"convertedAudio":     nilIfEmpty(d.ConvertTo),
		"audioIndex":         audioIndex,
		"audioTracks":        audio,
		"subtitles":          subsOut,
		"selectedSubtitleId": selectedSub,
		"trickplay":          s.trickplayInfo(it),
		"markers":            markers,
		"nextUpAtMs":         creditsAt,
		"resumeMs":           resume,
		"durationMs":         duration,
		"unsupportedReason":  unsupported,
		"warnings":           warnings,
	})
}

func hasEmbeddedText(stored []store.Subtitle) bool {
	for _, st := range stored {
		if _, ok := textSubExt(st.Codec); ok && st.Source == "embedded" {
			return true
		}
	}
	return false
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// directFallback replaces an HLS decision when audio can't be converted:
// pick another playable track (same language first, main tracks before
// commentary), or play the chosen one anyway with a warning.
func directFallback(d playback.Decision, info *probe.Info, warnings []string, forced bool) (playback.Decision, []string) {
	d.Mode, d.ConvertTo = "direct", ""
	var cur probe.Audio
	for _, a := range info.Audio {
		if a.Index == d.AudioIndex {
			cur = a
		}
	}
	if d.Playable[d.AudioIndex] {
		if forced {
			warnings = append(warnings, "Direct play failed on the device and audio conversion isn't available")
		}
		return d, warnings
	}
	// Same-language main track, then any main track, then commentaries.
	pick := -1
	for pass := 0; pass < 4 && pick < 0; pass++ {
		for _, a := range info.Audio {
			sameLang := a.Language == cur.Language
			commentary := strings.Contains(strings.ToLower(a.Title), "comment")
			if !d.Playable[a.Index] || (pass < 2 && commentary) || (pass%2 == 0 && !sameLang) {
				continue
			}
			pick = a.Index
			break
		}
	}
	if pick >= 0 {
		var alt probe.Audio
		for _, a := range info.Audio {
			if a.Index == pick {
				alt = a
			}
		}
		warnings = append(warnings, probe.AudioLabel(cur)+" can't be played on this device, so "+probe.AudioLabel(alt)+
			" is used instead (audio conversion isn't available)")
		d.AudioIndex = pick
		return d, warnings
	}
	warnings = append(warnings, probe.AudioLabel(cur)+" can't be played on this device and audio conversion isn't available, so this file may play without sound")
	return d, warnings
}

func containerOf(p string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(p)), ".")
	if ext == "m4v" {
		return "mp4"
	}
	return ext
}
