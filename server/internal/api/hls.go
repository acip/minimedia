package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/acip/minimedia/server/internal/fsutil"
	"github.com/acip/minimedia/server/internal/hls"
	"github.com/acip/minimedia/server/internal/probe"
	"github.com/acip/minimedia/server/internal/procutil"
	"github.com/acip/minimedia/server/internal/store"
	"github.com/acip/minimedia/server/internal/subs"
)

// ---- HLS with converted audio (PLAN.md 4.3) --------------------------------------

func hlsURL(itemID string, audioIndex int, codec string) string {
	return fmt.Sprintf("/stream/%s/hls/%d/%s/main.m3u8", itemID, audioIndex, codec)
}

// hlsSource resolves the rendition named by the request path.
func (s *Server) hlsSource(w http.ResponseWriter, r *http.Request) (hls.Source, bool) {
	if s.opt.HLS == nil {
		writeError(w, http.StatusNotFound, "audio conversion is not available")
		return hls.Source{}, false
	}
	it, err := s.store.GetItem(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return hls.Source{}, false
	}
	audio, err := strconv.Atoi(r.PathValue("audio"))
	if err != nil {
		writeError(w, http.StatusNotFound, "bad audio index")
		return hls.Source{}, false
	}
	info, err := probe.Parse([]byte(it.ProbeJSON))
	if err != nil {
		writeError(w, http.StatusConflict, "this file hasn't been analysed yet")
		return hls.Source{}, false
	}
	src, err := hls.SourceFor(it.ID, it.Path, it.Size, it.MtimeMs, info, audio, r.PathValue("codec"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return hls.Source{}, false
	}
	return src, true
}

// GET /stream/{id}/hls/{audio}/{codec}/main.m3u8
func (s *Server) hlsPlaylist(w http.ResponseWriter, r *http.Request) {
	src, ok := s.hlsSource(w, r)
	if !ok {
		return
	}
	pl, err := s.opt.HLS.Playlist(r.Context(), src)
	if err != nil {
		s.hlsError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(pl))
}

// GET /stream/{id}/hls/{audio}/{codec}/seg/{n}.ts
func (s *Server) hlsSegment(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutSuffix(r.PathValue("file"), ".ts")
	n, err := strconv.Atoi(name)
	if !ok || err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	src, ok := s.hlsSource(w, r)
	if !ok {
		return
	}
	path, err := s.opt.HLS.Segment(r.Context(), src, n)
	if err != nil {
		s.hlsError(w, r, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "video/mp2t")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func (s *Server) hlsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, hls.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case r.Context().Err() != nil:
		// The player gave up on the request (a seek); nothing to answer.
	default:
		s.log.Warn("hls request failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusServiceUnavailable, "audio conversion failed: "+err.Error())
	}
}

func (s *Server) hlsStatus() any {
	if s.opt.HLS == nil {
		return map[string]any{"enabled": false}
	}
	return struct {
		Enabled bool `json:"enabled"`
		hls.Status
	}{true, s.opt.HLS.Status()}
}

// ---- embedded text subtitles as files (PLAN.md 4.1, 4.4) ---------------------------

// textSubExt is the file type an embedded text subtitle is extracted to; ok
// is false for image subtitles (PGS, VobSub, DVB), which can't be.
func textSubExt(codec string) (ext string, ok bool) {
	switch codec {
	case "subrip", "srt", "mov_text", "text":
		return "srt", true
	case "ass", "ssa":
		return "ass", true
	case "webvtt":
		return "vtt", true
	}
	return "", false
}

func (s *Server) extractedPath(subID, ext string) string {
	return filepath.Join(s.opt.DataDir, "subs", subID+"."+ext)
}

type extractCall struct {
	done chan struct{}
	err  error
}

// extractOut is one embedded subtitle to extract.
type extractOut struct {
	streamIndex int
	encoder     string // ffmpeg subtitle encoder
	path        string // cache file
	language    string
}

// extractSubtitles extracts every embedded text subtitle of an item that isn't
// cached yet, in one pass over the file (the whole file is read, so this
// takes about as long as reading it from disk). Concurrent calls share it.
func (s *Server) extractSubtitles(ctx context.Context, it *store.Item, stored []store.Subtitle) error {
	encoders := map[string]string{"srt": "srt", "ass": "ass", "vtt": "webvtt"}
	var need []extractOut
	for _, sub := range stored {
		ext, ok := textSubExt(sub.Codec)
		if sub.Source != "embedded" || !ok {
			continue
		}
		p := s.extractedPath(sub.ID, ext)
		if fi, err := os.Stat(p); err == nil && !fi.ModTime().Before(time.UnixMilli(it.MtimeMs)) {
			continue
		}
		need = append(need, extractOut{sub.StreamIndex, encoders[ext], p, sub.Language})
	}
	if len(need) == 0 {
		return nil
	}

	s.extractMu.Lock()
	c, running := s.extracts[it.ID]
	if !running {
		c = &extractCall{done: make(chan struct{})}
		s.extracts[it.ID] = c
		go func() {
			c.err = s.runExtract(it, need)
			s.extractMu.Lock()
			delete(s.extracts, it.ID)
			s.extractMu.Unlock()
			close(c.done)
		}()
	}
	s.extractMu.Unlock()
	select {
	case <-c.done:
		return c.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runExtract runs one ffmpeg with an output per subtitle, then converts each
// result like a sidecar (Romanian cedilla fixes) and moves it into the cache.
func (s *Server) runExtract(it *store.Item, outs []extractOut) error {
	if s.opt.FFmpeg == "" {
		return errors.New("ffmpeg is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := os.MkdirAll(s.opt.DataDir, 0o755); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(s.opt.DataDir, "subs-extract-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	args := []string{"-nostdin", "-loglevel", "error", "-i", fsutil.LongPath(it.Path)}
	tmps := make([]string, len(outs))
	for i, o := range outs {
		tmps[i] = filepath.Join(tmpDir, strconv.Itoa(i)+filepath.Ext(o.path))
		args = append(args, "-map", "0:"+strconv.Itoa(o.streamIndex), "-c:s", o.encoder, tmps[i])
	}
	start := time.Now()
	cmd := procutil.Command(ctx, s.opt.FFmpeg, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := procutil.StartLow(cmd); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		s.log.Warn("subtitle extraction failed", "path", it.Path, "err", err, "ffmpeg", strings.TrimSpace(stderr.String()))
		return fmt.Errorf("subtitle extraction failed: %w", err)
	}
	for i, o := range outs {
		data, err := os.ReadFile(tmps[i])
		if err != nil {
			return err
		}
		text, err := subs.ToUTF8(data, o.language, s.opt.FallbackEncoding)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(o.path, []byte(text)); err != nil {
			return err
		}
	}
	s.log.Info("embedded subtitles extracted", "path", it.Path, "tracks", len(outs), "took", time.Since(start).Round(time.Millisecond))
	return nil
}

// WarmSubtitles extracts an item's embedded text subtitles if they aren't
// cached yet (background work ahead of HLS playback).
func (s *Server) WarmSubtitles(ctx context.Context, itemID string) {
	it, err := s.store.GetItem(ctx, itemID)
	if err != nil {
		return
	}
	stored, err := s.store.Subtitles(ctx, itemID)
	if err == nil && hasEmbeddedText(stored) {
		_ = s.extractSubtitles(ctx, it, stored)
	}
}

// serveEmbeddedSubtitle answers /subs/{item}/{sub}.{ext} for an embedded track.
func (s *Server) serveEmbeddedSubtitle(w http.ResponseWriter, r *http.Request, sub store.Subtitle, ext string) {
	want, ok := textSubExt(sub.Codec)
	if !ok {
		writeError(w, http.StatusNotFound, "image subtitles (PGS, VobSub) can only be shown from the video container in direct play")
		return
	}
	if ext != want {
		writeError(w, http.StatusNotFound, "this subtitle is only available as ."+want)
		return
	}
	ctx := r.Context()
	it, err := s.store.GetItem(ctx, sub.ItemID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	stored, err := s.store.Subtitles(ctx, it.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.extractSubtitles(ctx, it, stored); err != nil {
		if ctx.Err() == nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
		}
		return
	}
	f, err := os.Open(s.extractedPath(sub.ID, ext))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Content-Type", subs.MIME(ext)+"; charset=utf-8")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}
