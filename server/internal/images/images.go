// Package images resolves, fetches and resizes artwork (PLAN.md 3.8):
// local file → TMDB → frame grab, served resized from a disk cache.
package images

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode .webp artwork

	"github.com/acip/minimedia/server/internal/ffutil"
	"github.com/acip/minimedia/server/internal/fsutil"
	"github.com/acip/minimedia/server/internal/probe"
	"github.com/acip/minimedia/server/internal/procutil"
	"github.com/acip/minimedia/server/internal/store"
)

// Widths are the sizes the API serves (?w=). 0 = the original, capped at 1920.
var Widths = []int{342, 780, 1280, 1920}

const (
	maxWidth     = 1920
	maxPixels    = 60_000_000 // refuse to decode anything bigger (decompression bombs)
	maxDownload  = 25 << 20
	frameWidth   = 1280
	framePercent = 20
	failureTTL   = time.Hour
)

// Kinds are the image kinds the API knows.
var Kinds = map[string]bool{"poster": true, "backdrop": true, "logo": true, "still": true}

// ErrNoImage means the owner has no image of that kind (and no fallback).
var ErrNoImage = errors.New("no image")

type Service struct {
	store    *store.Store
	dir      string // data_dir/images
	ffmpeg   string
	log      *slog.Logger
	client   *http.Client
	TMDBBase string // image CDN prefix, e.g. https://image.tmdb.org/t/p/original

	frameSem  chan struct{}
	resizeSem chan struct{}

	mu       sync.Mutex
	inflight map[string]*call
	failed   map[string]time.Time // frame grabs/downloads that failed recently
}

type call struct {
	done chan struct{}
	err  error
}

func New(st *store.Store, dataDir, ffmpeg string, log *slog.Logger) *Service {
	return &Service{
		store: st, dir: filepath.Join(dataDir, "images"), ffmpeg: ffmpeg, log: log,
		client:    &http.Client{Timeout: 60 * time.Second},
		TMDBBase:  "https://image.tmdb.org/t/p/original",
		frameSem:  make(chan struct{}, 2),
		resizeSem: make(chan struct{}, max(1, runtime.NumCPU()/2)),
		inflight:  map[string]*call{},
		failed:    map[string]time.Time{},
	}
}

// URL is the API path of an image; v changes whenever the image does, so
// clients can cache it for a long time. Clients append &w=<width>.
func URL(ownerID, kind, version string) string {
	return "/images/" + ownerID + "/" + kind + "?v=" + version
}

// FrameVersion is the version of a frame grab taken from a file in this state.
func FrameVersion(mtimeMs, size int64) string {
	return "f" + hash(strconv.FormatInt(mtimeMs, 10) + "|" + strconv.FormatInt(size, 10))[:7]
}

// source is where an image comes from.
type source struct {
	kind    string // local | tmdb | frame
	path    string // local file, TMDB file_path, or the video for frame grabs
	version string
	item    *store.Item // frame grabs
}

func (s *Service) resolve(ctx context.Context, ownerID, kind string) (*source, error) {
	im, err := s.store.BestImage(ctx, ownerID, kind)
	if err == nil {
		return &source{kind: im.Source, path: im.Path, version: im.Version}, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if kind != "backdrop" && kind != "still" {
		return nil, ErrNoImage
	}
	// Frame grab: movie backdrop, episode still, or a show's first episode.
	it, err := s.store.GetItem(ctx, ownerID)
	if err == nil {
		if (it.Kind == "movie" && kind == "backdrop") || (it.Kind == "episode" && kind == "still") {
			return frameSource(it)
		}
		return nil, ErrNoImage
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if kind != "backdrop" {
		return nil, ErrNoImage
	}
	if _, err := s.store.GetShow(ctx, ownerID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNoImage
		}
		return nil, err
	}
	eps, err := s.store.Episodes(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	for _, e := range eps {
		if e.RuntimeMs > 0 {
			full, err := s.store.GetItem(ctx, e.ID)
			if err != nil {
				return nil, err
			}
			return frameSource(full)
		}
	}
	return nil, ErrNoImage
}

func frameSource(it *store.Item) (*source, error) {
	if it.RuntimeMs <= 0 || it.ProbeJSON == "" {
		return nil, ErrNoImage // not probed (yet)
	}
	return &source{kind: "frame", path: it.Path, version: FrameVersion(it.MtimeMs, it.Size), item: it}, nil
}

// Serve writes the image of ownerID/kind resized to width (one of Widths, or 0).
func (s *Service) Serve(w http.ResponseWriter, r *http.Request, ownerID, kind string, width int) error {
	src, err := s.resolve(r.Context(), ownerID, kind)
	if err != nil {
		return err
	}
	ext := ".jpg"
	if kind == "logo" {
		ext = ".png" // keep transparency
	}
	file, err := s.cached(r.Context(), ownerID, kind, src, width, ext)
	if err != nil {
		return err
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	ct := "image/jpeg"
	if ext == ".png" {
		ct = "image/png"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Header().Set("ETag", `"`+src.version+"-"+strconv.Itoa(width)+`"`)
	http.ServeContent(w, r, "", fi.ModTime(), f) // handles If-None-Match → 304
	return nil
}

// cached returns the resized image file, building it once if needed.
func (s *Service) cached(ctx context.Context, ownerID, kind string, src *source, width int, ext string) (string, error) {
	dir := filepath.Join(s.dir, "cache", ownerID)
	prefix := kind + "-" + src.version + "-"
	file := s.cacheFile(ownerID, kind, src, width, ext)
	if _, err := os.Stat(file); err == nil {
		return file, nil
	}
	err := s.once(file, func() error {
		orig, err := s.original(ctx, ownerID, src)
		if err != nil {
			return err
		}
		select {
		case s.resizeSem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		defer func() { <-s.resizeSem }()
		data, err := resize(orig, width, ext == ".png")
		if err != nil {
			return err
		}
		if err := writeAtomic(file, data); err != nil {
			return err
		}
		// Drop sizes of older versions of this image.
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if n := e.Name(); strings.HasPrefix(n, kind+"-") && !strings.HasPrefix(n, prefix) {
					os.Remove(filepath.Join(dir, n))
				}
			}
		}
		return nil
	})
	return file, err
}

// cacheFile is where the resized image of ownerID/kind at width is kept.
func (s *Service) cacheFile(ownerID, kind string, src *source, width int, ext string) string {
	return filepath.Join(s.dir, "cache", ownerID, kind+"-"+src.version+"-"+strconv.Itoa(width)+ext)
}

// original returns the full-size image bytes' location on disk.
func (s *Service) original(ctx context.Context, ownerID string, src *source) (string, error) {
	switch src.kind {
	case "local":
		return src.path, nil // read straight from the library (read-only)
	case "tmdb":
		ext := path.Ext(src.path)
		if ext == "" || len(ext) > 5 {
			ext = ".jpg"
		}
		file := filepath.Join(s.dir, "src", "tmdb", hash(src.path)+ext)
		if _, err := os.Stat(file); err == nil {
			return file, nil
		}
		return file, s.once(file, func() error { return s.download(ctx, s.TMDBBase+src.path, file) })
	case "frame":
		file := filepath.Join(s.dir, "src", "frame", ownerID+"-"+src.version+".jpg")
		if _, err := os.Stat(file); err == nil {
			return file, nil
		}
		return file, s.once(file, func() error { return s.grab(ctx, src.item, file) })
	}
	return "", fmt.Errorf("unknown image source %q", src.kind)
}

// once runs fn for key unless it is already running (then waits for it), and
// remembers failures for failureTTL so broken files aren't retried per request.
func (s *Service) once(key string, fn func() error) error {
	s.mu.Lock()
	if t, ok := s.failed[key]; ok {
		if time.Since(t) < failureTTL {
			s.mu.Unlock()
			return ErrNoImage
		}
		delete(s.failed, key)
	}
	if c, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		<-c.done
		return c.err
	}
	c := &call{done: make(chan struct{})}
	s.inflight[key] = c
	s.mu.Unlock()

	c.err = fn()
	s.mu.Lock()
	delete(s.inflight, key)
	if c.err != nil && !errors.Is(c.err, context.Canceled) {
		s.failed[key] = time.Now()
	}
	s.mu.Unlock()
	close(c.done)
	return c.err
}

func (s *Service) download(ctx context.Context, url, file string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Warn("image download failed", "url", url, "err", err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.log.Warn("image download failed", "url", url, "status", resp.Status)
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return err
	}
	if len(data) > maxDownload {
		return fmt.Errorf("download %s: too large", url)
	}
	return writeAtomic(file, data)
}

// grab extracts a frame 20% into the video, tone-mapped if HDR (PLAN.md 3.8).
func (s *Service) grab(ctx context.Context, it *store.Item, file string) error {
	select {
	case s.frameSem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.frameSem }()

	info, err := probe.Parse([]byte(it.ProbeJSON))
	if err != nil || info.Video == nil {
		return ErrNoImage
	}
	vf := frameFilter(info.Video, ffutil.HasFilter(s.ffmpeg, "zscale"))
	at := float64(it.RuntimeMs) * framePercent / 100 / 1000
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".tmp.jpg"
	// Context of its own: a client hanging up shouldn't waste a finished grab.
	gctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := procutil.Command(gctx, s.ffmpeg, "-nostdin", "-loglevel", "error", "-y",
		"-ss", strconv.FormatFloat(at, 'f', 3, 64), "-i", fsutil.LongPath(it.Path),
		"-map", "0:"+strconv.Itoa(info.Video.Index), "-frames:v", "1", "-an", "-sn", "-dn",
		"-vf", vf, "-q:v", "3", tmp)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		s.log.Warn("frame grab failed", "path", it.Path, "err", err, "ffmpeg", strings.TrimSpace(stderr.String()))
		return ErrNoImage
	}
	if err := os.Rename(tmp, file); err != nil {
		os.Remove(tmp)
		return err
	}
	// Remove grabs of older versions of the file.
	if old, err := filepath.Glob(filepath.Join(filepath.Dir(file), it.ID+"-*.jpg")); err == nil {
		for _, o := range old {
			if o != file {
				os.Remove(o)
			}
		}
	}
	return nil
}

// frameFilter scales to 1280 px wide using the display aspect ratio (so
// anamorphic sources come out right), with tone mapping for HDR.
func frameFilter(v *probe.Video, zscale bool) string {
	h := "-2"
	if v.DisplayAspect > 0 {
		h = strconv.Itoa(int(math.Round(frameWidth/v.DisplayAspect/2)) * 2)
	}
	vf := "scale=" + strconv.Itoa(frameWidth) + ":" + h + ",setsar=1"
	if v.HDR != "" && v.HDR != "sdr" && zscale {
		vf = ffutil.ToneMap + "," + vf
	}
	return vf
}

// resize decodes an image and scales it down to width (0 = original, capped
// at maxWidth). Images are never scaled up.
func resize(file string, width int, keepAlpha bool) ([]byte, error) {
	f, err := fsutil.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", filepath.Base(file), err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("decode %s: unsupported size %dx%d", filepath.Base(file), cfg.Width, cfg.Height)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", filepath.Base(file), err)
	}
	b := src.Bounds()
	target := width
	if target <= 0 {
		target = maxWidth
	}
	target = min(target, b.Dx())
	h := max(1, int(math.Round(float64(b.Dy())*float64(target)/float64(b.Dx()))))
	var out bytes.Buffer
	if keepAlpha {
		dst := image.NewNRGBA(image.Rect(0, 0, target, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, dst)
	} else {
		dst := image.NewRGBA(image.Rect(0, 0, target, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
		err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85})
	}
	return out.Bytes(), err
}

// Warm makes frame grabs ahead of time for movies without a backdrop and
// episodes without a still, so the TV's first browse isn't slow. Run it after
// metadata, so TMDB artwork is preferred over frames.
func (s *Service) Warm(ctx context.Context) {
	items, err := s.store.AllItems(ctx)
	if err != nil {
		return
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	refs, err := s.store.ImageRefs(ctx, ids)
	if err != nil {
		return
	}
	n := 0
	for _, it := range items {
		if ctx.Err() != nil {
			return
		}
		kind := "backdrop"
		if it.Kind == "episode" {
			kind = "still"
		}
		if _, ok := refs[it.ID][kind]; ok || it.RuntimeMs <= 0 {
			continue
		}
		file := filepath.Join(s.dir, "src", "frame", it.ID+"-"+FrameVersion(it.MtimeMs, it.Size)+".jpg")
		if _, err := os.Stat(file); err == nil {
			continue
		}
		full, err := s.store.GetItem(ctx, it.ID)
		if err != nil {
			continue
		}
		src, err := frameSource(full)
		if err != nil {
			continue
		}
		if _, err := s.original(ctx, it.ID, src); err == nil {
			n++
		}
	}
	if n > 0 {
		s.log.Info("frame grabs made", "count", n)
	}
	s.warmSizes(ctx, items)
}

// AppSizes are the widths the TV app requests for each kind: posters on
// cards; backdrops full-screen and on landscape cards; logos in the hero;
// stills on landscape cards and in episode lists.
var AppSizes = map[string][]int{
	"poster":   {342},
	"backdrop": {1280, 780},
	"logo":     {780},
	"still":    {780, 342},
}

// warmSizes resizes every image to the sizes the app asks for, so browsing a
// screen for the first time doesn't wait for resizing (100–600 ms per image).
// Sizes already on disk are skipped quickly.
func (s *Service) warmSizes(ctx context.Context, items []*store.Item) {
	owners := make([]string, 0, len(items))
	for _, it := range items {
		owners = append(owners, it.ID)
	}
	if shows, err := s.store.ListShows(ctx, "title", 0); err == nil {
		for _, sh := range shows {
			owners = append(owners, sh.ID)
		}
	}
	n := 0
	for _, id := range owners {
		for kind, widths := range AppSizes {
			if ctx.Err() != nil {
				return
			}
			src, err := s.resolve(ctx, id, kind)
			if err != nil {
				continue
			}
			ext := ".jpg"
			if kind == "logo" {
				ext = ".png"
			}
			for _, w := range widths {
				if _, err := os.Stat(s.cacheFile(id, kind, src, w, ext)); err == nil {
					continue
				}
				if _, err := s.cached(ctx, id, kind, src, w, ext); err == nil {
					n++
				}
			}
		}
	}
	if n > 0 {
		s.log.Info("image sizes prepared", "count", n)
	}
}

func writeAtomic(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".tmp-*")
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
	if err := os.Rename(tmp.Name(), file); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

func hash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
