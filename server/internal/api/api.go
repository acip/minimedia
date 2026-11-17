// Package api is the JSON/HTTP API used by the TV app (PLAN.md section 6).
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/encoding"

	"github.com/acip/minimedia/server/internal/hls"
	"github.com/acip/minimedia/server/internal/images"
	"github.com/acip/minimedia/server/internal/library"
	"github.com/acip/minimedia/server/internal/metadata"
	"github.com/acip/minimedia/server/internal/power"
	"github.com/acip/minimedia/server/internal/store"
	"github.com/acip/minimedia/server/internal/trickplay"
)

// Options configures the API server.
type Options struct {
	Name               string
	Version            string
	ServerID           string
	DataDir            string
	FallbackEncoding   encoding.Encoding
	PreferredLanguages []string
	AudioConvert       string // auto | always | never

	Images    *images.Service
	Metadata  *metadata.Fetcher    // nil when no TMDB key is configured
	Trickplay *trickplay.Generator // nil in tests that don't need it
	HLS       *hls.Manager         // nil: no audio conversion (files that need it play direct with a warning)
	Power     *power.Keeper        // nil in tests: streaming keeps the PC awake
	FFmpeg    string               // for extracting embedded subtitles
}

type Server struct {
	opt     Options
	store   *store.Store
	scanner *library.Scanner
	images  *images.Service
	log     *slog.Logger
	now     func() time.Time

	extractMu sync.Mutex
	extracts  map[string]*extractCall // embedded subtitle extraction per item
}

func New(opt Options, st *store.Store, sc *library.Scanner, log *slog.Logger) *Server {
	if opt.AudioConvert == "" {
		opt.AudioConvert = "auto"
	}
	return &Server{opt: opt, store: st, scanner: sc, images: opt.Images, log: log, now: time.Now,
		extracts: map[string]*extractCall{}}
}

// Handler returns the HTTP handler with all routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", s.info)
	mux.HandleFunc("GET /api/home", s.home)
	mux.HandleFunc("GET /api/movies", s.movies)
	mux.HandleFunc("GET /api/shows", s.shows)
	mux.HandleFunc("GET /api/shows/{id}", s.show)
	mux.HandleFunc("POST /api/shows/{id}/watched", s.showWatched(true))
	mux.HandleFunc("DELETE /api/shows/{id}/watched", s.showWatched(false))
	mux.HandleFunc("GET /api/shows/{id}/prefs", s.getPrefs)
	mux.HandleFunc("PUT /api/shows/{id}/prefs", s.putPrefs)
	mux.HandleFunc("GET /api/items/{id}", s.item)
	mux.HandleFunc("POST /api/items/{id}/playback", s.playback)
	mux.HandleFunc("PUT /api/items/{id}/progress", s.putProgress)
	mux.HandleFunc("POST /api/items/{id}/watched", s.itemWatched(true))
	mux.HandleFunc("DELETE /api/items/{id}/watched", s.itemWatched(false))
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("POST /api/library/scan", s.scan)
	mux.HandleFunc("GET /api/library/status", s.status)
	mux.HandleFunc("GET /api/library/unmatched", s.unmatched)
	mux.HandleFunc("GET /stream/{id}/file", s.streamFile) // GET patterns also match HEAD
	mux.HandleFunc("GET /stream/{id}/hls/{audio}/{codec}/main.m3u8", s.hlsPlaylist)
	mux.HandleFunc("GET /stream/{id}/hls/{audio}/{codec}/seg/{file}", s.hlsSegment)
	mux.HandleFunc("GET /subs/{itemId}/{file}", s.subtitle)
	mux.HandleFunc("GET /images/{ownerId}/{kind}", s.image)
	mux.HandleFunc("GET /trickplay/{id}/{file}", s.trickplay)
	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		if strings.HasPrefix(r.URL.Path, "/api/") && acceptsGzip(r) {
			gz := &gzipJSON{ResponseWriter: w}
			defer gz.Close() // deferred first, so it runs after the handler and error handling
			w = gz
		}
		rw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		if p := s.opt.Power; p != nil {
			// Playing keeps the PC awake: streams while they're open, and
			// the app's progress reports (PLAN.md 7).
			if strings.HasPrefix(r.URL.Path, "/stream/") {
				defer p.Begin()()
			} else if strings.HasSuffix(r.URL.Path, "/progress") {
				p.Touch()
			}
		}
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				s.log.Error("panic in handler", "path", r.URL.Path, "panic", rec, "stack", string(debug.Stack()))
				if !rw.wrote {
					writeError(rw, http.StatusInternalServerError, "internal error")
				}
			}
			s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", rw.status, "took", time.Since(start).Round(time.Microsecond))
		}()
		next.ServeHTTP(rw, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status  int
	wrote   bool
	swallow bool // a plain-text mux error was replaced with JSON
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.status, w.wrote = code, true
	// The mux answers unknown paths (404) and wrong methods (405) with
	// http.Error's text/plain body; the API promises {"error": "..."}.
	if code >= 400 && strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		msg := strings.ToLower(http.StatusText(code))
		w.Header().Del("X-Content-Type-Options")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.ResponseWriter.WriteHeader(code)
		_ = json.NewEncoder(w.ResponseWriter).Encode(map[string]string{"error": msg})
		w.swallow = true
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.swallow {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController and ServeContent reach the real writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail maps store errors to responses.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Context().Err() != nil {
		return // client went away
	}
	s.log.Error("request failed", "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, err.Error())
}

func decodeBody(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"name": s.opt.Name, "version": s.opt.Version, "serverId": s.opt.ServerID, "os": runtime.GOOS,
	})
}

func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	s.scanner.Trigger()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "scan requested"})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	var meta any = metadata.Status{Enabled: false}
	if s.opt.Metadata != nil {
		meta = s.opt.Metadata.Status(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scan":      s.scanner.Status(),
		"metadata":  meta,
		"trickplay": s.trickplayStatus(),
		"hls":       s.hlsStatus(),
	})
}

func (s *Server) unmatched(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.scanner.Unmatched())
}
