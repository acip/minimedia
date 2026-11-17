// Package trickplay generates seek-bar thumbnail sprite sheets (PLAN.md 5):
// in the background, newest items first, at below-normal priority.
package trickplay

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/acip/minimedia/server/internal/ffutil"
	"github.com/acip/minimedia/server/internal/fsutil"
	"github.com/acip/minimedia/server/internal/probe"
	"github.com/acip/minimedia/server/internal/procutil"
	"github.com/acip/minimedia/server/internal/store"
)

const (
	Columns  = 5
	Rows     = 5
	perSheet = Columns * Rows
)

// Index is index.json (PLAN.md 5.1).
type Index struct {
	IntervalMs int64  `json:"intervalMs"`
	TileWidth  int    `json:"tileWidth"`
	TileHeight int    `json:"tileHeight"`
	Columns    int    `json:"columns"`
	Rows       int    `json:"rows"`
	Count      int    `json:"count"`
	Sheets     int    `json:"sheets"`
	Version    string `json:"version"`
}

type Options struct {
	Enabled         bool
	IntervalSeconds int
	Width           int
	MaxParallelJobs int
}

// Status is reported by GET /api/library/status.
type Status struct {
	Enabled bool     `json:"enabled"`
	Running []string `json:"running"` // item ids being generated
	Pending int      `json:"pending"`
	Ready   int      `json:"ready"`
	Failed  int      `json:"failed"`
	ToneMap bool     `json:"toneMap"` // zscale available for HDR sources
}

type Generator struct {
	store  *store.Store
	dir    string // data_dir/trickplay
	ffmpeg string
	opt    Options
	log    *slog.Logger
	kick   chan struct{}

	mu      sync.Mutex
	running map[string]bool
	status  Status
}

func New(st *store.Store, dataDir, ffmpeg string, opt Options, log *slog.Logger) *Generator {
	if opt.MaxParallelJobs < 1 {
		opt.MaxParallelJobs = 1
	}
	return &Generator{
		store: st, dir: filepath.Join(dataDir, "trickplay"), ffmpeg: ffmpeg, opt: opt, log: log,
		kick: make(chan struct{}, 1), running: map[string]bool{},
		status: Status{Enabled: opt.Enabled},
	}
}

// Version identifies the thumbnails of a file in its current state with the
// current settings; a change means they must be regenerated.
func (g *Generator) Version(it *store.Item) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%d|%d|%d|%d", it.MtimeMs, it.Size, g.opt.IntervalSeconds, g.opt.Width)))
	return hex.EncodeToString(sum[:])[:12]
}

// Index returns the thumbnail index of an item if it is ready and current.
func (g *Generator) Index(it *store.Item) (*Index, bool) {
	data, err := os.ReadFile(filepath.Join(g.dir, it.ID, "index.json"))
	if err != nil {
		return nil, false
	}
	var idx Index
	if json.Unmarshal(data, &idx) != nil || idx.Version != g.Version(it) {
		return nil, false
	}
	return &idx, true
}

// SheetPath is the file of sheet n of an item (the caller checks readiness).
func (g *Generator) SheetPath(id string, n int) string {
	return filepath.Join(g.dir, id, strconv.Itoa(n)+".jpg")
}

func (g *Generator) Status() Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.status
	st.Running = make([]string, 0, len(g.running))
	for id := range g.running {
		st.Running = append(st.Running, id)
	}
	sort.Strings(st.Running)
	return st
}

// Kick asks for a pass over the library (after scans).
func (g *Generator) Kick() {
	select {
	case g.kick <- struct{}{}:
	default:
	}
}

// Run processes kicks until ctx is cancelled.
func (g *Generator) Run(ctx context.Context) {
	if !g.opt.Enabled {
		return
	}
	g.mu.Lock()
	g.status.ToneMap = ffutil.HasFilter(g.ffmpeg, "zscale")
	g.mu.Unlock()
	if !g.status.ToneMap {
		g.log.Warn("ffmpeg has no zscale filter; HDR thumbnails will look washed out (use a full ffmpeg build)")
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.kick:
		}
		if err := g.pass(ctx); err != nil && ctx.Err() == nil {
			g.log.Warn("trickplay pass failed", "err", err)
		}
	}
}

// pass generates whatever is missing or stale, newest items first.
func (g *Generator) pass(ctx context.Context) error {
	items, err := g.store.AllItems(ctx)
	if err != nil {
		return err
	}
	states, err := g.store.TrickplayStates(ctx)
	if err != nil {
		return err
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].AddedAt > items[j].AddedAt })

	var todo []*store.Item
	ready, failed := 0, 0
	keep := map[string]bool{}
	for _, it := range items {
		keep[it.ID] = true
		if it.RuntimeMs <= 0 {
			continue // not probed (yet)
		}
		v := g.Version(it)
		switch states[it.ID] {
		case "ready:" + v:
			if _, ok := g.Index(it); ok {
				ready++
				continue
			}
		case "failed:" + v:
			failed++
			continue
		}
		todo = append(todo, it)
	}
	g.cleanup(keep)
	g.mu.Lock()
	g.status.Pending, g.status.Ready, g.status.Failed = len(todo), ready, failed
	g.mu.Unlock()
	if len(todo) == 0 {
		return nil
	}
	g.log.Info("generating seek thumbnails", "items", len(todo))

	jobs := make(chan *store.Item)
	var wg sync.WaitGroup
	for i := 0; i < g.opt.MaxParallelJobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range jobs {
				g.one(ctx, it)
			}
		}()
	}
	for _, it := range todo {
		select {
		case jobs <- it:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	return ctx.Err()
}

func (g *Generator) one(ctx context.Context, it *store.Item) {
	g.mu.Lock()
	g.running[it.ID] = true
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.running, it.ID)
		g.status.Pending--
		g.mu.Unlock()
	}()
	start := time.Now()
	v := g.Version(it)
	idx, err := g.Generate(ctx, it)
	if ctx.Err() != nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err != nil {
		g.log.Warn("seek thumbnails failed", "path", it.Path, "err", err)
		g.status.Failed++
		_ = g.store.SetTrickplayState(context.Background(), it.ID, "failed:"+v)
		return
	}
	g.status.Ready++
	_ = g.store.SetTrickplayState(context.Background(), it.ID, "ready:"+v)
	g.log.Info("seek thumbnails ready", "path", it.Path, "thumbnails", idx.Count, "sheets", idx.Sheets,
		"took", time.Since(start).Round(time.Millisecond))
}

// Generate makes the sprite sheets of one item (PLAN.md 5.2): only keyframes
// are decoded (-skip_frame nokey), one per interval, tone-mapped for HDR.
// Output goes to a temporary folder that replaces the old one when complete.
func (g *Generator) Generate(ctx context.Context, it *store.Item) (*Index, error) {
	full, err := g.store.GetItem(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	info, err := probe.Parse([]byte(full.ProbeJSON))
	if err != nil || info.Video == nil {
		return nil, errors.New("no video stream")
	}
	w := g.opt.Width
	h := TileHeight(w, info.Video.DisplayAspect)
	interval := g.opt.IntervalSeconds

	// format=yuvj420p: full-range output, as JPEG expects (tone-mapped frames
	// are limited-range and the encoder refuses them).
	vf := fmt.Sprintf("fps=1/%d:eof_action=pass,showinfo,scale=%d:%d,setsar=1,tile=%dx%d,format=yuvj420p",
		interval, w, h, Columns, Rows)
	if info.Video.HDR != "" && info.Video.HDR != "sdr" && ffutil.HasFilter(g.ffmpeg, "zscale") {
		vf = ffutil.ToneMap + "," + vf
	}

	if err := os.MkdirAll(g.dir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(g.dir, ".tmp-"+it.ID+"-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp) // gone after the rename; cleans up failures

	want := int(math.Ceil(float64(full.RuntimeMs) / 1000 / float64(interval)))
	// Decoding only keyframes is far faster, but FFmpeg's HEVC decoder outputs
	// nothing for open-GOP streams (CRA keyframes, x265's default) that way.
	// Fall back to a full decode when the fast pass comes up short.
	count, sheets, err := g.run(ctx, full.Path, info.Video.Index, vf, tmp, true)
	if err != nil || count < want-1 {
		g.log.Debug("keyframe-only decode came up short; decoding everything", "path", full.Path, "count", count, "err", err)
		clearDir(tmp)
		count, sheets, err = g.run(ctx, full.Path, info.Video.Index, vf, tmp, false)
	}
	if err != nil {
		return nil, err
	}
	if count == 0 || sheets != (count+perSheet-1)/perSheet {
		return nil, fmt.Errorf("unexpected output: %d thumbnails in %d sheets", count, sheets)
	}
	if count < want-1 || count > want+1 {
		g.log.Warn("thumbnail count differs from duration", "path", full.Path, "count", count, "expected", want)
	}
	idx := &Index{IntervalMs: int64(interval) * 1000, TileWidth: w, TileHeight: h, Columns: Columns, Rows: Rows,
		Count: count, Sheets: sheets, Version: g.Version(full)}
	data, _ := json.MarshalIndent(idx, "", "  ")
	if err := os.WriteFile(filepath.Join(tmp, "index.json"), data, 0o644); err != nil {
		return nil, err
	}
	final := filepath.Join(g.dir, it.ID)
	if err := os.RemoveAll(final); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, final); err != nil {
		return nil, err
	}
	return idx, nil
}

// run executes one ffmpeg pass into dir and returns the thumbnail and sheet counts.
func (g *Generator) run(ctx context.Context, path string, stream int, vf, dir string, keyframesOnly bool) (int, int, error) {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "info"}
	if keyframesOnly {
		args = append(args, "-skip_frame", "nokey")
	}
	args = append(args, "-i", fsutil.LongPath(path),
		"-map", "0:"+strconv.Itoa(stream), "-an", "-sn", "-dn",
		"-vf", vf, "-q:v", "5", "-fps_mode", "vfr", "-start_number", "0",
		filepath.Join(dir, "%d.jpg"))
	cmd := procutil.Command(ctx, g.ffmpeg, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 0, 0, err
	}
	if err := procutil.StartLow(cmd); err != nil {
		return 0, 0, err
	}
	count, tail := countFrames(stderr)
	if err := cmd.Wait(); err != nil {
		return count, 0, fmt.Errorf("ffmpeg: %v: %s", err, tail)
	}
	sheets := 0
	for {
		if _, err := os.Stat(filepath.Join(dir, strconv.Itoa(sheets)+".jpg")); err != nil {
			break
		}
		sheets++
	}
	return count, sheets, nil
}

func clearDir(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		os.Remove(filepath.Join(dir, e.Name()))
	}
}

// TileHeight keeps the display aspect ratio (anamorphic sources included),
// rounded to an even number.
func TileHeight(width int, displayAspect float64) int {
	if displayAspect <= 0 {
		displayAspect = 16.0 / 9
	}
	return int(math.Round(float64(width)/displayAspect/2)) * 2
}

// countFrames reads ffmpeg's stderr, counting showinfo lines (one per
// thumbnail), and keeps the last lines for error messages.
func countFrames(r io.Reader) (int, string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	n := 0
	var last []string
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "Parsed_showinfo") && strings.Contains(line, " n:") {
			n++
			continue
		}
		last = append(last, line)
		if len(last) > 5 {
			last = last[1:]
		}
	}
	return n, strings.TrimSpace(strings.Join(last, " | "))
}

// cleanup removes thumbnails of items that no longer exist and stale temp folders.
func (g *Generator) cleanup(keep map[string]bool) {
	entries, err := os.ReadDir(g.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".tmp-") {
			g.mu.Lock()
			busy := false
			for id := range g.running {
				if strings.HasPrefix(name, ".tmp-"+id+"-") {
					busy = true
				}
			}
			g.mu.Unlock()
			if !busy {
				os.RemoveAll(filepath.Join(g.dir, name))
			}
			continue
		}
		if e.IsDir() && !keep[name] {
			os.RemoveAll(filepath.Join(g.dir, name))
		}
	}
}
