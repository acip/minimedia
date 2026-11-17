package trickplay

import (
	"context"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/acip/minimedia/server/internal/config"
	"github.com/acip/minimedia/server/internal/library"
	"github.com/acip/minimedia/server/internal/store"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// setup scans testdata/media into a fresh store (run testdata/generate.ps1 first).
func setup(t *testing.T) (*store.Store, string, string, map[string]*store.Item) {
	t.Helper()
	media, _ := filepath.Abs(filepath.Join("..", "..", "..", "testdata", "media"))
	if _, err := os.Stat(filepath.Join(media, "Movies")); err != nil {
		t.Skip("testdata/media not generated; run testdata/generate.ps1")
	}
	ffmpeg, err1 := exec.LookPath("ffmpeg")
	ffprobe, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg/ffprobe not on PATH")
	}
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sc := library.NewScanner(st, []config.Library{
		{Name: "Movies", Type: "movies", Path: filepath.Join(media, "Movies")},
		{Name: "Shows", Type: "shows", Path: filepath.Join(media, "Shows")},
	}, ffprobe, quiet())
	if _, err := sc.ScanAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, _ := st.AllItems(context.Background())
	byTitle := map[string]*store.Item{}
	for _, it := range items {
		byTitle[it.Title] = it
	}
	return st, dir, ffmpeg, byTitle
}

func decode(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func tile(sheet image.Image, i, w, h int) image.Image {
	i %= perSheet
	x, y := (i%Columns)*w, (i/Columns)*h
	return sheet.(interface {
		SubImage(image.Rectangle) image.Image
	}).SubImage(image.Rect(x, y, x+w, y+h))
}

// diff is the mean absolute per-channel difference of two same-size images (0…1).
func diff(a, b image.Image) float64 {
	ab, bb := a.Bounds(), b.Bounds()
	var sum float64
	n := 0
	for y := 0; y < ab.Dy(); y += 4 {
		for x := 0; x < ab.Dx(); x += 4 {
			r1, g1, b1, _ := a.At(ab.Min.X+x, ab.Min.Y+y).RGBA()
			r2, g2, b2, _ := b.At(bb.Min.X+x, bb.Min.Y+y).RGBA()
			sum += (math.Abs(float64(r1)-float64(r2)) + math.Abs(float64(g1)-float64(g2)) + math.Abs(float64(b1)-float64(b2))) / 3 / 65535
			n++
		}
	}
	return sum / float64(n)
}

func saturation(img image.Image) float64 {
	b := img.Bounds()
	var sum float64
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			r, g, bl, _ := img.At(x, y).RGBA()
			sum += float64(max(r, g, bl)-min(r, g, bl)) / 65535
			n++
		}
	}
	return sum / float64(n)
}

func keyframes(t *testing.T, path string) []float64 {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-skip_frame", "nokey",
		"-show_entries", "frame=pts_time", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var kf []float64
	for _, l := range strings.Fields(strings.ReplaceAll(string(out), ",", " ")) {
		if v, err := strconv.ParseFloat(l, 64); err == nil {
			kf = append(kf, v)
		}
	}
	return kf
}

func TestSheetsMatchDurationAndKeyframes(t *testing.T) {
	st, dir, ffmpeg, items := setup(t)
	g := New(st, dir, ffmpeg, Options{Enabled: true, IntervalSeconds: 10, Width: 320, MaxParallelJobs: 1}, quiet())
	ctx := context.Background()

	for _, title := range []string{"Big Test Movie", "Irregular Keys", "Pilot"} {
		it := items[title]
		if it == nil {
			t.Fatalf("%s not scanned", title)
		}
		idx, err := g.Generate(ctx, it)
		if err != nil {
			t.Fatalf("%s: %v", title, err)
		}
		want := int(math.Ceil(float64(it.RuntimeMs) / 10000))
		if idx.Count != want || idx.Sheets != (want+24)/25 || idx.TileWidth != 320 || idx.TileHeight != 180 {
			t.Errorf("%s: index %+v, want %d thumbnails", title, idx, want)
		}
		if got, ok := g.Index(it); !ok || got.Count != idx.Count {
			t.Errorf("%s: index not readable back", title)
		}
		sheet := decode(t, g.SheetPath(it.ID, 0))
		if b := sheet.Bounds(); b.Dx() != 320*Columns || b.Dy() != 180*Rows {
			t.Errorf("%s: sheet is %dx%d", title, b.Dx(), b.Dy())
		}
	}

	// Alignment (PLAN.md 9, Phase 3): thumbnail i must show the keyframe
	// nearest to i×interval, i.e. within one keyframe gap of its slot.
	it := items["Irregular Keys"] // irregular keyframes on purpose
	kf := keyframes(t, it.Path)
	sheet := decode(t, g.SheetPath(it.ID, 0))
	frames := map[float64]image.Image{}
	tmp := t.TempDir()
	for i, k := range kf {
		out := filepath.Join(tmp, strconv.Itoa(i)+".jpg")
		if b, err := exec.Command(ffmpeg, "-v", "error", "-ss", strconv.FormatFloat(k, 'f', 3, 64), "-i", it.Path,
			"-frames:v", "1", "-vf", "scale=320:180", "-q:v", "5", out).CombinedOutput(); err != nil {
			t.Fatalf("grab %v: %v %s", k, err, b)
		}
		frames[k] = decode(t, out)
	}
	for i := 0; i < 6; i++ {
		slot := float64(i * 10)
		best, bestD := -1.0, math.MaxFloat64
		for k, img := range frames {
			if d := diff(tile(sheet, i, 320, 180), img); d < bestD {
				best, bestD = k, d
			}
		}
		gap := 0.0
		for j := 1; j < len(kf); j++ {
			if kf[j-1] <= slot+10 && kf[j] >= slot-10 {
				gap = max(gap, kf[j]-kf[j-1])
			}
		}
		if math.Abs(best-slot) > gap+0.5 {
			t.Errorf("thumbnail %d (t=%vs) shows keyframe %.2fs; keyframe gap %.2fs", i, slot, best, gap)
		}
		t.Logf("thumbnail %d (t=%2.0fs) = keyframe %6.2fs (diff %.3f)", i, slot, best, bestD)
	}
}

func TestHDRThumbnailsAreToneMapped(t *testing.T) {
	st, dir, ffmpeg, items := setup(t)
	g := New(st, dir, ffmpeg, Options{Enabled: true, IntervalSeconds: 10, Width: 320, MaxParallelJobs: 1}, quiet())
	it := items["HDR Sample"]
	idx, err := g.Generate(context.Background(), it)
	if err != nil {
		t.Fatal(err)
	}
	sheet := decode(t, g.SheetPath(it.ID, 0))
	var sat float64
	for i := 0; i < idx.Count; i++ {
		sat += saturation(tile(sheet, i, idx.TileWidth, idx.TileHeight))
	}
	sat /= float64(idx.Count)
	t.Logf("HDR thumbnails: mean saturation %.3f", sat)
	// Untouched PQ frames of this file measure ~0.22, tone-mapped ~0.55 (see images tests).
	if sat < 0.35 {
		t.Errorf("HDR thumbnails look washed out (saturation %.3f)", sat)
	}
}

func TestPassQueueStateAndCleanup(t *testing.T) {
	st, dir, ffmpeg, items := setup(t)
	ctx := context.Background()
	g := New(st, dir, ffmpeg, Options{Enabled: true, IntervalSeconds: 30, Width: 160, MaxParallelJobs: 2}, quiet())
	stray := filepath.Join(dir, "trickplay", "deadbeefdeadbeef")
	os.MkdirAll(stray, 0o755)
	if err := g.pass(ctx); err != nil {
		t.Fatal(err)
	}
	s := g.Status()
	if s.Ready != len(items)-0 && s.Ready+s.Failed != len(items) {
		t.Errorf("status after pass = %+v (items %d)", s, len(items))
	}
	if s.Failed != 0 || s.Pending != 0 || len(s.Running) != 0 {
		t.Errorf("status = %+v", s)
	}
	for title, it := range items {
		if _, ok := g.Index(it); !ok {
			t.Errorf("%s has no thumbnails", title)
		}
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Error("thumbnails of a missing item were not removed")
	}
	states, _ := st.TrickplayStates(ctx)
	if len(states) != len(items) {
		t.Errorf("states = %v", states)
	}

	// Nothing to do on a second pass; new settings make everything stale.
	if err := g.pass(ctx); err != nil {
		t.Fatal(err)
	}
	if s := g.Status(); s.Ready != len(items) {
		t.Errorf("second pass status = %+v", s)
	}
	g2 := New(st, dir, ffmpeg, Options{Enabled: true, IntervalSeconds: 20, Width: 160, MaxParallelJobs: 1}, quiet())
	if _, ok := g2.Index(items["Big Test Movie"]); ok {
		t.Error("thumbnails with another interval reported as current")
	}
}

func TestTileHeight(t *testing.T) {
	for _, c := range []struct {
		w    int
		dar  float64
		want int
	}{{320, 16.0 / 9, 180}, {320, 2.39, 134}, {320, 4.0 / 3, 240}, {320, 0, 180}} {
		if got := TileHeight(c.w, c.dar); got != c.want {
			t.Errorf("TileHeight(%d, %.2f) = %d, want %d", c.w, c.dar, got, c.want)
		}
	}
}
