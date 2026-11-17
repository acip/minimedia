package api

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/acip/minimedia/server/internal/fsutil"
	"github.com/acip/minimedia/server/internal/progress"
	"github.com/acip/minimedia/server/internal/subs"
)

// ---- progress ----------------------------------------------------------------

type progressIn struct {
	PositionMs *int64 `json:"positionMs"`
	DurationMs int64  `json:"durationMs"`
	Paused     bool   `json:"paused"`
}

func (s *Server) putProgress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in progressIn
	if err := decodeBody(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.PositionMs == nil || *in.PositionMs < 0 || in.DurationMs < 0 {
		writeError(w, http.StatusBadRequest, "positionMs is required and must be ≥ 0")
		return
	}
	it, err := s.store.GetItem(ctx, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	dur := in.DurationMs
	if dur == 0 {
		dur = it.RuntimeMs
	}
	credits, err := s.store.CreditsStart(ctx, it.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	now := s.now()
	st, err := s.store.UpdateProgress(ctx, it.ID, func(p progress.State) progress.State {
		return progress.Apply(p, *in.PositionMs, dur, credits, now)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, progressJSON(st))
}

func (s *Server) itemWatched(watched bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		it, err := s.store.GetItem(ctx, r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		st, err := s.store.UpdateProgress(ctx, it.ID, s.markFn(watched, it.RuntimeMs))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, progressJSON(st))
	}
}

// POST/DELETE /api/shows/{id}/watched[?season=N]
func (s *Server) showWatched(watched bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		sh, err := s.store.GetShow(ctx, r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		season := -1
		if v := r.URL.Query().Get("season"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "season must be a number ≥ 0")
				return
			}
			season = n
		}
		eps, err := s.store.Episodes(ctx, sh.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		count := 0
		for _, e := range eps {
			if season >= 0 && e.Season != season {
				continue
			}
			if _, err := s.store.UpdateProgress(ctx, e.ID, s.markFn(watched, e.RuntimeMs)); err != nil {
				s.fail(w, r, err)
				return
			}
			count++
		}
		if season >= 0 && count == 0 {
			writeError(w, http.StatusNotFound, "no episodes in season "+strconv.Itoa(season))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"updated": count, "watched": watched})
	}
}

func (s *Server) markFn(watched bool, runtimeMs int64) func(progress.State) progress.State {
	now := s.now()
	return func(p progress.State) progress.State {
		if p.DurationMs == 0 {
			p.DurationMs = runtimeMs
		}
		if watched {
			return progress.MarkWatched(p, now)
		}
		return progress.MarkUnwatched(p)
	}
}

// ---- direct play -------------------------------------------------------------

var videoTypes = map[string]string{
	".mkv": "video/x-matroska", ".mp4": "video/mp4", ".m4v": "video/mp4", ".mov": "video/quicktime",
	".avi": "video/x-msvideo", ".ts": "video/mp2t", ".m2ts": "video/mp2t", ".webm": "video/webm",
	".wmv": "video/x-ms-wmv",
}

// GET/HEAD /stream/{id}/file — Range, If-Range and HEAD via http.ServeContent.
func (s *Server) streamFile(w http.ResponseWriter, r *http.Request) {
	it, err := s.store.GetItem(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f, err := fsutil.Open(it.Path)
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		s.fileError(w, r, err)
		return
	}
	ct := videoTypes[strings.ToLower(filepath.Ext(it.Path))]
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func (s *Server) fileError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "file is missing on disk; a rescan will remove it")
		return
	}
	s.log.Error("open media file", "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "cannot read file")
}

// ---- subtitles ---------------------------------------------------------------

// GET /subs/{itemId}/{subId}.{ext} — external sidecars converted to UTF-8
// (PLAN.md 3.4/4.4), and embedded text tracks extracted (for HLS, which
// carries no subtitles), cached in data_dir/subs.
func (s *Server) subtitle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	file := r.PathValue("file")
	subID, ext, ok := strings.Cut(file, ".")
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	sub, err := s.store.Subtitle(ctx, r.PathValue("itemId"), subID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if sub.Source != "external" {
		s.serveEmbeddedSubtitle(w, r, *sub, ext)
		return
	}
	if ext != sub.Codec {
		writeError(w, http.StatusNotFound, "this subtitle is only available as ."+sub.Codec)
		return
	}
	src, err := fsutil.Stat(sub.Path)
	if err != nil {
		s.fileError(w, r, err)
		return
	}

	cacheDir := filepath.Join(s.opt.DataDir, "subs")
	cachePath := filepath.Join(cacheDir, sub.ID+"."+sub.Codec)
	if c, err := os.Stat(cachePath); err != nil || c.ModTime().Before(src.ModTime()) {
		data, err := os.ReadFile(fsutil.LongPath(sub.Path))
		if err != nil {
			s.fileError(w, r, err)
			return
		}
		text, err := subs.ToUTF8(data, sub.Language, s.opt.FallbackEncoding)
		if err != nil {
			s.log.Warn("subtitle conversion failed", "path", sub.Path, "err", err)
			writeError(w, http.StatusUnprocessableEntity, "subtitle file could not be decoded")
			return
		}
		if err := writeFileAtomic(cachePath, []byte(text)); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	f, err := os.Open(cachePath)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Content-Type", subs.MIME(sub.Codec)+"; charset=utf-8")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
