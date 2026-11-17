package api

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// gzipJSON compresses JSON responses for clients that accept gzip (the TV
// app's HTTP client always does, and decompresses transparently). A movie
// list of 2,000 titles is ~550 KB of JSON and ~10% of that compressed, which
// matters over a TV's Wi-Fi. Other responses (video, images, subtitles) pass
// through untouched: they are already compressed or need byte ranges.
type gzipJSON struct {
	http.ResponseWriter
	gz      *gzip.Writer
	decided bool
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if strings.EqualFold(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]), "gzip") {
			return true
		}
	}
	return false
}

func (w *gzipJSON) decide() {
	if w.decided {
		return
	}
	w.decided = true
	h := w.Header()
	if !strings.HasPrefix(h.Get("Content-Type"), "application/json") || h.Get("Content-Encoding") != "" {
		return
	}
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	h.Add("Vary", "Accept-Encoding")
	w.gz, _ = gzip.NewWriterLevel(w.ResponseWriter, gzip.BestSpeed)
}

func (w *gzipJSON) WriteHeader(code int) {
	if code != http.StatusNoContent && code != http.StatusNotModified && code >= 200 {
		w.decide()
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipJSON) Write(b []byte) (int, error) {
	if !w.decided {
		w.WriteHeader(http.StatusOK)
	}
	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// Close finishes the gzip stream, if one was started.
func (w *gzipJSON) Close() {
	if w.gz != nil {
		_ = w.gz.Close()
	}
}

// Unwrap lets http.ResponseController and ServeContent reach the real writer.
func (w *gzipJSON) Unwrap() http.ResponseWriter { return w.ResponseWriter }
