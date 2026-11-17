package hls

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/acip/minimedia/server/internal/probe"
)

func quiet() *slog.Logger {
	if os.Getenv("HLS_DEBUG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPlan(t *testing.T) {
	p := NewPlan([]float64{0, 2, 4, 6.5, 7, 13, 20, 26}, 25, 6)
	want := []float64{0, 6.5, 13, 20}
	if fmt.Sprint(p.Starts) != fmt.Sprint(want) {
		t.Fatalf("starts = %v, want %v", p.Starts, want)
	}
	durs := []float64{6.5, 6.5, 7, 5}
	sum := 0.0
	for i, d := range durs {
		if math.Abs(p.Duration(i)-d) > 1e-9 {
			t.Errorf("duration(%d) = %v, want %v", i, p.Duration(i), d)
		}
		sum += p.Duration(i)
	}
	if sum != 25 {
		t.Errorf("sum = %v", sum)
	}
	pl := p.Playlist()
	for _, s := range []string{"#EXT-X-PLAYLIST-TYPE:VOD", "#EXT-X-TARGETDURATION:7\n", "#EXTINF:6.500000,\nseg/1.ts\n", "#EXTINF:5.000000,\nseg/3.ts\n#EXT-X-ENDLIST\n"} {
		if !strings.Contains(pl, s) {
			t.Errorf("playlist lacks %q:\n%s", s, pl)
		}
	}
	// Sparse keyframes: segments are as long as the gaps.
	if p := NewPlan([]float64{0, 10, 30}, 40, 6); fmt.Sprint(p.Starts) != "[0 10 30]" || p.Duration(2) != 10 {
		t.Errorf("sparse: %+v", p)
	}
}

func TestParseKeyframes(t *testing.T) {
	in := "0.000000,K__\n0.041000,___\n2.002000,K_\nN/A,K__\n4.004000,K__,\n2.002000,K__\n"
	got := ParseKeyframes(strings.NewReader(in))
	if fmt.Sprint(got) != "[0 2.002 4.004]" {
		t.Errorf("got %v", got)
	}
}

func TestJobArgs(t *testing.T) {
	src := Source{Path: `C:\m\a.mkv`, VideoIndex: 0, AudioIndex: 2, Channels: 6, Codec: "ac3", StartSec: 0}
	p := NewPlan([]float64{0, 6, 12.5, 19, 25}, 30, 6)
	a := strings.Join(JobArgs(src, p, 2, p.Len(), `C:\d`), " ")
	for _, s := range []string{"-ss 12.499 -copyts", `noise=drop=lt(pts*tb\,12.499)`, "-segment_times 6.450,12.450 ",
		"-segment_start_number 2", "-c:a ac3 -b:a 640k -ac 6", "-map 0:0 -map 0:2"} {
		if !strings.Contains(a, s) {
			t.Errorf("args lack %q: %s", s, a)
		}
	}
	// From the start: no seek, no packet dropping.
	if a := strings.Join(JobArgs(src, p, 0, p.Len(), `C:\d`), " "); strings.Contains(a, "-ss") || strings.Contains(a, "noise") {
		t.Errorf("start args: %s", a)
	}
}

// ---- against generated media (testdata/generate.ps1) ---------------------------

type env struct {
	ffmpeg, ffprobe string
	media           string
}

func setup(t *testing.T) env {
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
	return env{ffmpeg, ffprobe, media}
}

func (e env) source(t *testing.T, rel, codec string) Source {
	t.Helper()
	path := filepath.Join(e.media, filepath.FromSlash(rel))
	raw, err := probe.Run(context.Background(), e.ffprobe, path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := probe.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	src, err := SourceFor("test"+strconv.Itoa(len(rel)), path, fi.Size(), fi.ModTime().UnixMilli(), info, info.Audio[0].Index, codec)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// packets lists (pts, keyframe) of one stream type ("v" or "a") in a file.
func (e env) packets(t *testing.T, file, kind string) [][2]float64 {
	t.Helper()
	out, err := exec.Command(e.ffprobe, "-v", "error", "-select_streams", kind+":0", "-show_entries", "packet=pts_time,flags",
		"-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", file, err)
	}
	var ps [][2]float64
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pts, flags, _ := strings.Cut(strings.TrimSpace(line), ",")
		f, err := strconv.ParseFloat(pts, 64)
		if err != nil {
			continue
		}
		k := 0.0
		if strings.Contains(flags, "K") {
			k = 1
		}
		ps = append(ps, [2]float64{f, k})
	}
	return ps
}

var extinfRe = regexp.MustCompile(`#EXTINF:([0-9.]+),`)

// PLAN.md Phase 4 checks: playlist durations sum to the file duration; every
// segment starts with the keyframe the playlist says; a far request (a seek)
// is served by a new job within seconds; the segments remux without errors.
func TestRenditions(t *testing.T) {
	e := setup(t)
	cases := []struct{ rel, codec string }{
		{"Movies/Irregular.Keys.2019.1080p.BluRay.x264-GRP.mkv", "eac3"}, // irregular keyframes, DTS
		{"Movies/HDR Sample (2020).mkv", "aac"},                          // HEVC 10-bit, open GOP
		{"Shows/Test Show (2020)/Season 01/Test Show - S01E01 - Pilot.mkv", "ac3"},
	}
	for _, c := range cases {
		t.Run(filepath.Base(c.rel), func(t *testing.T) {
			ctx := context.Background()
			src := e.source(t, c.rel, c.codec)
			m := New(ctx, t.TempDir(), e.ffmpeg, e.ffprobe, quiet())

			pl, err := m.Playlist(ctx, src)
			if err != nil {
				t.Fatal(err)
			}
			sum := 0.0
			var durs []float64
			for _, sm := range extinfRe.FindAllStringSubmatch(pl, -1) {
				d, _ := strconv.ParseFloat(sm[1], 64)
				durs = append(durs, d)
				sum += d
			}
			if math.Abs(sum-src.DurationSec) > 0.1 {
				t.Errorf("playlist sums to %.3f s, file is %.3f s", sum, src.DurationSec)
			}
			st, _ := m.stream(ctx, src)
			plan := st.plan
			if plan.Len() < 3 {
				t.Fatalf("only %d segments", plan.Len())
			}

			// A seek: the last segments first, then everything in order.
			far := plan.Len() - 2
			start := time.Now()
			if _, err := m.Segment(ctx, src, far); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("far segment took %v", d)
			}
			files := make([]string, plan.Len())
			for n := 0; n < plan.Len(); n++ {
				p, err := m.Segment(ctx, src, n)
				if err != nil {
					t.Fatalf("segment %d: %v", n, err)
				}
				files[n] = p
				v := e.packets(t, p, "v")
				if len(v) == 0 {
					t.Fatalf("segment %d has no video", n)
				}
				first := v[0]
				for _, pk := range v {
					if pk[1] == 1 {
						first = pk
						break
					}
				}
				want := plan.Starts[n] + TSOffsetSec
				if v[0][1] != 1 || math.Abs(first[0]-want) > 0.002 {
					t.Errorf("segment %d starts with %v (keyframe %v), want keyframe at %.3f", n, v[0][0], v[0][1] == 1, want)
				}
				for _, pk := range v {
					if pk[1] == 1 && n+1 < plan.Len() && pk[0] >= plan.Starts[n+1]+TSOffsetSec-0.002 {
						t.Errorf("segment %d holds the next segment's keyframe %.3f", n, pk[0])
						break
					}
				}
				if a := e.packets(t, p, "a"); len(a) == 0 {
					t.Errorf("segment %d has no audio", n)
				}
			}

			// Remux the whole rendition through the playlist.
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "seg"), 0o755); err != nil {
				t.Fatal(err)
			}
			for n, f := range files {
				data, _ := os.ReadFile(f)
				os.WriteFile(filepath.Join(dir, "seg", strconv.Itoa(n)+".ts"), data, 0o644)
			}
			os.WriteFile(filepath.Join(dir, "main.m3u8"), []byte(pl), 0o644)
			cmd := exec.Command(e.ffmpeg, "-nostdin", "-v", "error", "-i", filepath.Join(dir, "main.m3u8"), "-c", "copy", filepath.Join(dir, "out.mkv"))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil || stderr.Len() > 0 {
				t.Errorf("remux: %v: %s", err, stderr.String())
			}
			raw, err := probe.Run(ctx, e.ffprobe, filepath.Join(dir, "out.mkv"))
			if err != nil {
				t.Fatal(err)
			}
			out, _ := probe.Parse(raw)
			if math.Abs(float64(out.DurationMs)/1000-src.DurationSec) > 0.25 {
				t.Errorf("remuxed duration %.3f s, source %.3f s", float64(out.DurationMs)/1000, src.DurationSec)
			}
			if len(out.Audio) != 1 || out.Audio[0].Codec != c.codec {
				t.Errorf("remuxed audio = %+v, want %s", out.Audio, c.codec)
			}
		})
	}
}

// A job stops when no one asks for segments, and a later request restarts it.
func TestIdleJobStops(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	src := e.source(t, "Shows/Test Show (2020)/Season 01/Test Show - S01E01 - Pilot.mkv", "aac")
	m := New(ctx, t.TempDir(), e.ffmpeg, e.ffprobe, quiet())
	if _, err := m.Segment(ctx, src, 1); err != nil {
		t.Fatal(err)
	}
	m.stopIdle(time.Now().Add(idleKill + time.Second))
	deadline := time.Now().Add(5 * time.Second)
	for m.Status().Jobs > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if j := m.Status().Jobs; j != 0 {
		t.Errorf("jobs = %d after idle stop", j)
	}
	st, _ := m.stream(ctx, src)
	last := st.plan.Len() - 1
	if _, err := m.Segment(ctx, src, last); err != nil {
		t.Fatalf("after idle stop: %v", err)
	}
}

// Old segment folders are deleted, and the cache is kept under its cap
// (least recently used first); folders of active streams stay.
func TestSweep(t *testing.T) {
	dir := t.TempDir()
	m := New(context.Background(), dir, "ffmpeg", "ffprobe", quiet())
	now := time.Now()
	mk := func(name string, age time.Duration, size int) {
		p := filepath.Join(m.dir, name)
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, "00000.ts"), make([]byte, size), 0o644)
		os.Chtimes(p, now.Add(-age), now.Add(-age))
	}
	mk("stale", idleDelete+time.Hour, 10)
	mk("recent", time.Minute, 10)
	mk("active", idleDelete+time.Hour, 10)
	m.streams["active"] = &stream{lastUsed: now}
	m.sweep(now)
	for name, want := range map[string]bool{"stale": false, "recent": true, "active": true} {
		if _, err := os.Stat(filepath.Join(m.dir, name)); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", name, err == nil, want)
		}
	}
}
