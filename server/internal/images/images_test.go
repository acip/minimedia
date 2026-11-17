package images

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/acip/minimedia/server/internal/ffutil"
	"github.com/acip/minimedia/server/internal/probe"
	"github.com/acip/minimedia/server/internal/store"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func writeJPEG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (*Service, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ffmpeg, _ := exec.LookPath("ffmpeg")
	return New(st, filepath.Join(dir, "data"), ffmpeg, quiet()), st, dir
}

func get(t *testing.T, s *Service, owner, kind string, width int, hdr http.Header) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/images/"+owner+"/"+kind, nil)
	for k, v := range hdr {
		r.Header[k] = v
	}
	w := httptest.NewRecorder()
	if err := s.Serve(w, r, owner, kind, width); err != nil {
		w.Code = 404
		w.Body.WriteString(err.Error())
	}
	return w
}

func TestLocalImageResizedCachedAndETag(t *testing.T) {
	s, st, dir := setup(t)
	ctx := context.Background()
	poster := filepath.Join(dir, "poster.jpg")
	writeJPEG(t, poster, 1000, 1500)
	tx, _ := st.Begin(ctx)
	tx.ReplaceImages("m1", "local", []store.Image{{Kind: "poster", Path: poster, Version: "v1"}})
	tx.Commit()

	w := get(t, s, "m1", "poster", 342, nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	cfg, err := jpeg.DecodeConfig(w.Body)
	if err != nil || cfg.Width != 342 || cfg.Height != 513 {
		t.Errorf("resized to %dx%d (%v), want 342x513", cfg.Width, cfg.Height, err)
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "max-age=604800") || w.Header().Get("ETag") != `"v1-342"` {
		t.Errorf("headers = %v", w.Header())
	}
	// Conditional request → 304.
	w2 := get(t, s, "m1", "poster", 342, http.Header{"If-None-Match": {`"v1-342"`}})
	if w2.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: status %d", w2.Code)
	}
	// Never upscaled; no width = original (≤1920).
	if w := get(t, s, "m1", "poster", 1920, nil); w.Code == 200 {
		if cfg, _ := jpeg.DecodeConfig(w.Body); cfg.Width != 1000 {
			t.Errorf("upscaled to %d", cfg.Width)
		}
	}
	// The source changes (new version) → old cached sizes are dropped.
	writeJPEG(t, poster, 800, 1200)
	tx, _ = st.Begin(ctx)
	tx.ReplaceImages("m1", "local", []store.Image{{Kind: "poster", Path: poster, Version: "v2"}})
	tx.Commit()
	if w := get(t, s, "m1", "poster", 342, nil); w.Code != 200 || w.Header().Get("ETag") != `"v2-342"` {
		t.Errorf("v2: %d %s", w.Code, w.Header().Get("ETag"))
	}
	entries, _ := os.ReadDir(filepath.Join(s.dir, "cache", "m1"))
	for _, e := range entries {
		if strings.Contains(e.Name(), "-v1-") {
			t.Errorf("stale cache file kept: %s", e.Name())
		}
	}
	// Unknown owner/kind → ErrNoImage.
	if w := get(t, s, "nope", "poster", 0, nil); w.Code != 404 {
		t.Errorf("missing poster: %d", w.Code)
	}
}

func TestLogoKeepsTransparency(t *testing.T) {
	s, st, dir := setup(t)
	ctx := context.Background()
	img := image.NewNRGBA(image.Rect(0, 0, 800, 200))
	img.Set(10, 10, color.NRGBA{255, 0, 0, 128})
	var b bytes.Buffer
	png.Encode(&b, img)
	logo := filepath.Join(dir, "clearlogo.png")
	os.WriteFile(logo, b.Bytes(), 0o644)
	tx, _ := st.Begin(ctx)
	tx.ReplaceImages("m1", "local", []store.Image{{Kind: "logo", Path: logo, Version: "l1"}})
	tx.Commit()
	w := get(t, s, "m1", "logo", 342, nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("logo: %d %v", w.Code, w.Header())
	}
	out, err := png.Decode(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := out.At(0, 0).RGBA(); a != 0 {
		t.Errorf("transparent pixel became opaque (alpha %d)", a)
	}
}

func TestTMDBDownloadOnceAndPriority(t *testing.T) {
	s, st, dir := setup(t)
	ctx := context.Background()
	var hits int
	jpg := filepath.Join(dir, "cdn.jpg")
	writeJPEG(t, jpg, 1280, 720)
	data, _ := os.ReadFile(jpg)
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/abc.jpg" {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	defer cdn.Close()
	s.TMDBBase = cdn.URL
	st.ReplaceImages(ctx, "m1", "tmdb", []store.Image{{Kind: "backdrop", Path: "/abc.jpg", Version: "t1"}})
	for _, width := range []int{780, 1280, 780} {
		if w := get(t, s, "m1", "backdrop", width, nil); w.Code != 200 {
			t.Fatalf("w=%d: %d %s", width, w.Code, w.Body.String())
		}
	}
	if hits != 1 {
		t.Errorf("CDN hit %d times, want 1", hits)
	}
	// A local file beats TMDB.
	local := filepath.Join(dir, "fanart.jpg")
	writeJPEG(t, local, 640, 360)
	tx, _ := st.Begin(ctx)
	tx.ReplaceImages("m1", "local", []store.Image{{Kind: "backdrop", Path: local, Version: "L"}})
	tx.Commit()
	if w := get(t, s, "m1", "backdrop", 780, nil); w.Header().Get("ETag") != `"L-780"` {
		t.Errorf("local did not win: %s", w.Header().Get("ETag"))
	}
	// A broken CDN path fails and is not retried for a while.
	st.ReplaceImages(ctx, "m2", "tmdb", []store.Image{{Kind: "poster", Path: "/missing.jpg", Version: "t2"}})
	get(t, s, "m2", "poster", 342, nil)
	before := hits
	if w := get(t, s, "m2", "poster", 342, nil); w.Code != 404 || hits != before {
		t.Errorf("failed download retried immediately (code %d, hits %d→%d)", w.Code, before, hits)
	}
}

func ffutilHasZscale(ffmpeg string) bool { return ffutil.HasFilter(ffmpeg, "zscale") }

// saturation is the mean (max−min) channel spread, 0 (grey) … 1.
func saturation(img image.Image) float64 {
	var sum float64
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 8 {
		for x := b.Min.X; x < b.Max.X; x += 8 {
			r, g, bl, _ := img.At(x, y).RGBA()
			sum += float64(max(r, g, bl)-min(r, g, bl)) / 65535
			n++
		}
	}
	return sum / float64(n)
}

func TestFrameFilter(t *testing.T) {
	sdr := &probe.Video{HDR: "sdr", DisplayAspect: 16.0 / 9}
	if got := frameFilter(sdr, true); got != "scale=1280:720,setsar=1" {
		t.Errorf("sdr = %q", got)
	}
	ana := &probe.Video{HDR: "sdr", DisplayAspect: 4.0 / 3} // e.g. 720x576 with a 16:15 SAR
	if got := frameFilter(ana, true); got != "scale=1280:960,setsar=1" {
		t.Errorf("anamorphic = %q", got)
	}
	hdr := &probe.Video{HDR: "hdr10", DisplayAspect: 2.39}
	if got := frameFilter(hdr, true); !strings.HasPrefix(got, "zscale=t=linear") || !strings.HasSuffix(got, "scale=1280:536,setsar=1") {
		t.Errorf("hdr = %q", got)
	}
	if got := frameFilter(hdr, false); strings.Contains(got, "zscale") {
		t.Errorf("hdr without zscale = %q", got)
	}
}

// TestFrameGrabs uses testdata/media (run testdata/generate.ps1) and ffmpeg.
func TestFrameGrabs(t *testing.T) {
	media, _ := filepath.Abs(filepath.Join("..", "..", "..", "testdata", "media"))
	hdrFile := filepath.Join(media, "Movies", "HDR Sample (2020).mkv")
	if _, err := os.Stat(hdrFile); err != nil {
		t.Skip("testdata/media not generated")
	}
	ffprobe, err1 := exec.LookPath("ffprobe")
	s, st, _ := setup(t)
	if s.ffmpeg == "" || err1 != nil {
		t.Skip("ffmpeg/ffprobe not on PATH")
	}
	ctx := context.Background()
	raw, err := probe.Run(ctx, ffprobe, hdrFile)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := probe.Parse(raw)
	fi, _ := os.Stat(hdrFile)
	tx, _ := st.Begin(ctx)
	tx.UpsertItem(&store.Item{ID: "hdr", Library: "M", Kind: "movie", Path: hdrFile, RelPath: "x", Size: fi.Size(),
		MtimeMs: fi.ModTime().UnixMilli(), Title: "HDR", SortTitle: "hdr", RuntimeMs: info.DurationMs, ProbeJSON: string(raw),
		AddedAt: time.Now().UnixMilli()})
	tx.Commit()

	start := time.Now()
	w := get(t, s, "hdr", "backdrop", 780, nil)
	if w.Code != 200 {
		t.Fatalf("frame grab: %d %s", w.Code, w.Body.String())
	}
	img, err := jpeg.Decode(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 780 {
		t.Errorf("width %d", b.Dx())
	}
	t.Logf("HDR frame grab + resize took %v", time.Since(start).Round(time.Millisecond))
	if !strings.HasPrefix(w.Header().Get("ETag"), `"`+FrameVersion(fi.ModTime().UnixMilli(), fi.Size())) {
		t.Errorf("etag = %s", w.Header().Get("ETag"))
	}
	// Washed-out check: the same frame without tone mapping (PQ values shown
	// as if they were BT.709) is visibly flatter and greyer.
	if ffutilHasZscale(s.ffmpeg) {
		plainFile := filepath.Join(t.TempDir(), "plain.jpg")
		cmd := exec.Command(s.ffmpeg, "-nostdin", "-loglevel", "error", "-ss", "6", "-i", hdrFile, "-frames:v", "1",
			"-vf", frameFilter(info.Video, false), plainFile)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("plain grab: %v %s", err, out)
		}
		pf, _ := os.Open(plainFile)
		plain, err := jpeg.Decode(pf)
		pf.Close()
		if err != nil {
			t.Fatal(err)
		}
		tm, raw := saturation(img), saturation(plain)
		t.Logf("mean saturation: tone-mapped %.3f, untouched %.3f", tm, raw)
		if tm < raw*1.15 {
			t.Errorf("tone mapping did not restore colour (%.3f vs %.3f)", tm, raw)
		}
	}
	// Posters have no frame fallback.
	if w := get(t, s, "hdr", "poster", 342, nil); w.Code != 404 {
		t.Errorf("poster fallback: %d", w.Code)
	}
}

func TestWarmSizes(t *testing.T) {
	s, st, dir := setup(t)
	ctx := context.Background()
	poster := filepath.Join(dir, "poster.jpg")
	backdrop := filepath.Join(dir, "fanart.jpg")
	writeJPEG(t, poster, 1000, 1500)
	writeJPEG(t, backdrop, 1920, 1080)
	tx, _ := st.Begin(ctx)
	tx.ReplaceImages("m1", "local", []store.Image{
		{Kind: "poster", Path: poster, Version: "p1"},
		{Kind: "backdrop", Path: backdrop, Version: "b1"},
	})
	tx.Commit()

	s.warmSizes(ctx, []*store.Item{{ID: "m1"}})
	for _, f := range []string{"poster-p1-342.jpg", "backdrop-b1-1280.jpg", "backdrop-b1-780.jpg"} {
		if _, err := os.Stat(filepath.Join(s.dir, "cache", "m1", f)); err != nil {
			t.Errorf("%s not prepared: %v", f, err)
		}
	}
	// Served from what was prepared (same file, no resize on request).
	if w := get(t, s, "m1", "backdrop", 780, nil); w.Code != 200 || w.Header().Get("ETag") != `"b1-780"` {
		t.Errorf("backdrop 780: %d %s", w.Code, w.Header().Get("ETag"))
	}
}
