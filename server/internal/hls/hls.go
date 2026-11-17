// Package hls streams files whose audio the device can't play as VOD HLS:
// the video is copied, the audio converted, and segments are cut exactly on
// source keyframes so the playlist is complete and accurate before any
// segment exists (PLAN.md 4.3).
//
// How a segment job cuts precisely (verified against ffmpeg 7.1, see the
// tests): the input is seeked to just before the segment's keyframe, video
// packets before it are dropped with the noise bitstream filter (stream copy
// otherwise starts at whatever keyframe the container index lands on), and
// the segment muxer gets the following boundaries relative to that first
// packet, slightly early so float rounding can't push a cut to the next
// keyframe. -copyts keeps source timestamps (plus TSOffsetSec), so segments
// from different jobs line up.
package hls

import (
	"bufio"
	"bytes"
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
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/acip/minimedia/server/internal/fsutil"
	"github.com/acip/minimedia/server/internal/probe"
	"github.com/acip/minimedia/server/internal/procutil"
)

const (
	// TargetSegmentSec is the minimum segment length; a segment closes at the
	// first keyframe at least this far after its start.
	TargetSegmentSec = 6.0
	cutMargin        = 0.05 // boundaries passed to the muxer this much early
	seekMargin       = 0.001
	// TSOffsetSec is added to every output timestamp. MPEG-TS can't carry
	// negative timestamps (B-frame DTS, audio encoder priming at the start of
	// a file), and ffmpeg would otherwise shift only the first segment of a
	// job, so segments from different jobs wouldn't line up.
	TSOffsetSec = 10

	lookahead  = 10               // a running job serves requests up to this many segments ahead
	waitReady  = 20 * time.Second // longest a segment request waits for its job
	idleKill   = 2 * time.Minute  // jobs without requests are stopped
	idleDelete = 6 * time.Hour    // segment folders unused this long are deleted
	cacheCap   = 20 << 30         // total segment cache size
	// Boundaries per job: -segment_times must fit on a Windows command line
	// (32k chars). A job stops after this many segments; the next request
	// starts a new one there.
	maxTimesPerJob = 1500

	formatVersion = "1" // bump when segment contents change
)

// ErrNotFound is returned for a segment number outside the playlist.
var ErrNotFound = errors.New("no such segment")

// Source describes one HLS rendition: a file and the audio track converted.
type Source struct {
	ID          string
	Path        string
	Size        int64
	MtimeMs     int64
	StartSec    float64 // container start time
	DurationSec float64
	VideoIndex  int
	AudioIndex  int
	Channels    int    // of the source audio track
	Codec       string // aac | ac3 | eac3
}

// SourceFor describes the rendition of a probed file with one audio track
// converted to codec.
func SourceFor(id, path string, size, mtimeMs int64, info *probe.Info, audioIndex int, codec string) (Source, error) {
	switch codec {
	case "aac", "ac3", "eac3":
	default:
		return Source{}, fmt.Errorf("unsupported audio codec %q", codec)
	}
	if info == nil || info.Video == nil {
		return Source{}, errors.New("the file has no analysed video stream")
	}
	src := Source{ID: id, Path: path, Size: size, MtimeMs: mtimeMs,
		StartSec: float64(info.StartMs) / 1000, DurationSec: float64(info.DurationMs) / 1000,
		VideoIndex: info.Video.Index, AudioIndex: -1, Codec: codec}
	for _, a := range info.Audio {
		if a.Index == audioIndex {
			src.AudioIndex, src.Channels = a.Index, a.Channels
		}
	}
	if src.AudioIndex < 0 {
		return Source{}, fmt.Errorf("no audio stream %d", audioIndex)
	}
	if src.DurationSec <= 0 {
		return Source{}, errors.New("the file's duration is unknown")
	}
	return src, nil
}

func (s Source) fileVersion() string {
	return hash(fmt.Sprintf("%d|%d", s.Size, s.MtimeMs))
}

// key names the rendition's segment folder.
func (s Source) key() string {
	return fmt.Sprintf("%s-%d-%s-%s", s.ID, s.AudioIndex, s.Codec,
		hash(s.fileVersion()+"|"+formatVersion+"|"+strconv.FormatFloat(TargetSegmentSec, 'f', -1, 64)))
}

func hash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// AudioArgs are the ffmpeg encoder options for the converted audio.
func AudioArgs(codec string, channels int) []string {
	switch codec {
	case "ac3", "eac3":
		ch := min(max(channels, 1), 6)
		br := "640k"
		switch {
		case ch <= 2:
			br = "224k"
		case ch < 5:
			br = "448k"
		}
		return []string{"-c:a", codec, "-b:a", br, "-ac", strconv.Itoa(ch), "-ar", "48000"}
	}
	return []string{"-c:a", "aac", "-b:a", "256k", "-ac", "2"}
}

// ---- segment plan ------------------------------------------------------------

// Plan is the list of segments: start times (absolute source seconds, each a
// keyframe) and the end of the last one.
type Plan struct {
	Starts []float64
	End    float64
}

// NewPlan walks the keyframes, closing a segment at the first keyframe at
// least target seconds after its start. end is the source end time.
func NewPlan(keyframes []float64, end, target float64) Plan {
	var p Plan
	for _, k := range keyframes {
		if k >= end {
			break
		}
		if len(p.Starts) == 0 || k >= p.Starts[len(p.Starts)-1]+target {
			p.Starts = append(p.Starts, k)
		}
	}
	p.End = end
	if n := len(p.Starts); n > 0 && p.End <= p.Starts[n-1] {
		p.End = p.Starts[n-1] + target
	}
	return p
}

// Len is the number of segments.
func (p Plan) Len() int { return len(p.Starts) }

// Duration of segment i in seconds.
func (p Plan) Duration(i int) float64 {
	if i+1 < len(p.Starts) {
		return p.Starts[i+1] - p.Starts[i]
	}
	return p.End - p.Starts[i]
}

// Playlist renders the VOD playlist (segment URIs relative to it).
func (p Plan) Playlist() string {
	maxDur := 0.0
	for i := range p.Starts {
		maxDur = math.Max(maxDur, p.Duration(i))
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-PLAYLIST-TYPE:VOD\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", int(math.Ceil(maxDur)))
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	for i := range p.Starts {
		fmt.Fprintf(&b, "#EXTINF:%.6f,\nseg/%d.ts\n", p.Duration(i), i)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// ---- manager -----------------------------------------------------------------

// Manager owns keyframe indexes, segment folders and the ffmpeg jobs.
type Manager struct {
	ffmpeg, ffprobe string
	dir, kfDir      string
	log             *slog.Logger
	ctx             context.Context // bounds jobs and indexing (server lifetime)

	mu       sync.Mutex
	streams  map[string]*stream
	kfCalls  map[string]*kfCall
	indexing string // item being indexed in the background
	pending  int    // background index queue length
	kick     chan struct{}

	// Pending lists files to index in the background (those likely to need
	// conversion); set by the caller.
	Pending func(ctx context.Context) ([]Source, error)
	// AfterIndex, if set, runs after each background-indexed file (e.g. to
	// extract its embedded subtitles ahead of time).
	AfterIndex func(ctx context.Context, itemID string)
}

type stream struct {
	src      Source
	dir      string
	plan     Plan
	ready    map[int]string // segment → file
	job      *job
	lastUsed time.Time
	changed  chan struct{} // closed and replaced on every change
}

type job struct {
	start, last int // first segment, last finished one (start-1 = none yet)
	stopAt      int // segments ≥ this aren't trusted (end of the boundary list)
	dir         string
	cancel      context.CancelFunc
	done        chan struct{}
	err         error
}

type kfCall struct {
	done chan struct{}
	kf   []float64
	err  error
}

// New creates the manager; dataDir/hls holds segments and dataDir/keyframes
// the keyframe indexes. ctx bounds all background work.
func New(ctx context.Context, dataDir, ffmpeg, ffprobe string, log *slog.Logger) *Manager {
	return &Manager{
		ffmpeg: ffmpeg, ffprobe: ffprobe, log: log, ctx: ctx,
		dir:     filepath.Join(dataDir, "hls"),
		kfDir:   filepath.Join(dataDir, "keyframes"),
		streams: map[string]*stream{},
		kfCalls: map[string]*kfCall{},
		kick:    make(chan struct{}, 1),
	}
}

// ---- keyframe index ----------------------------------------------------------

type kfFile struct {
	Version   string    `json:"version"`
	Keyframes []float64 `json:"keyframes"`
}

func (m *Manager) kfPath(id string) string { return filepath.Join(m.kfDir, id+".json") }

func (m *Manager) cachedKeyframes(src Source) ([]float64, bool) {
	data, err := os.ReadFile(m.kfPath(src.ID))
	if err != nil {
		return nil, false
	}
	var f kfFile
	if json.Unmarshal(data, &f) != nil || f.Version != src.fileVersion() || len(f.Keyframes) == 0 {
		return nil, false
	}
	return f.Keyframes, true
}

// Keyframes returns the video keyframe times of a file, from the cache or by
// reading every video packet with ffprobe (demux only: about as fast as the
// disk). Concurrent calls for one file share the work.
func (m *Manager) Keyframes(ctx context.Context, src Source) ([]float64, error) {
	return m.keyframes(ctx, src, false)
}

func (m *Manager) keyframes(ctx context.Context, src Source, background bool) ([]float64, error) {
	if kf, ok := m.cachedKeyframes(src); ok {
		return kf, nil
	}
	m.mu.Lock()
	c, ok := m.kfCalls[src.ID]
	if !ok {
		c = &kfCall{done: make(chan struct{})}
		m.kfCalls[src.ID] = c
		go func() {
			start := time.Now()
			c.kf, c.err = m.buildKeyframes(m.ctx, src, background)
			if c.err == nil {
				c.err = writeJSONAtomic(m.kfPath(src.ID), kfFile{Version: src.fileVersion(), Keyframes: c.kf})
				m.log.Info("keyframe index built", "path", src.Path, "keyframes", len(c.kf),
					"took", time.Since(start).Round(time.Millisecond), "background", background)
			} else if m.ctx.Err() == nil {
				m.log.Warn("keyframe index failed", "path", src.Path, "err", c.err)
			}
			m.mu.Lock()
			delete(m.kfCalls, src.ID)
			m.mu.Unlock()
			close(c.done)
		}()
	}
	m.mu.Unlock()
	select {
	case <-c.done:
		return c.kf, c.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *Manager) buildKeyframes(ctx context.Context, src Source, low bool) ([]float64, error) {
	cmd := procutil.Command(ctx, m.ffprobe, "-v", "error", "-select_streams", strconv.Itoa(src.VideoIndex),
		"-show_entries", "packet=pts_time,flags", "-of", "csv=p=0", fsutil.LongPath(src.Path))
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if low {
		err = procutil.StartLow(cmd)
	} else {
		err = cmd.Start()
	}
	if err != nil {
		return nil, err
	}
	kf := ParseKeyframes(out)
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("ffprobe: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if len(kf) == 0 {
		return nil, errors.New("no video keyframes found")
	}
	return kf, nil
}

// ParseKeyframes reads ffprobe "pts_time,flags" lines and returns the sorted
// times of packets flagged K.
func ParseKeyframes(r io.Reader) []float64 {
	var kf []float64
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		pts, flags, ok := strings.Cut(strings.TrimSpace(sc.Text()), ",")
		if !ok || !strings.Contains(flags, "K") {
			continue
		}
		t, err := strconv.ParseFloat(pts, 64)
		if err != nil {
			continue
		}
		kf = append(kf, t)
	}
	sort.Float64s(kf)
	out := kf[:0]
	for i, t := range kf {
		if i == 0 || t-out[len(out)-1] > 1e-6 {
			out = append(out, t)
		}
	}
	return out
}

// HasKeyframes reports whether a file's keyframe index is cached.
func (m *Manager) HasKeyframes(src Source) bool {
	_, ok := m.cachedKeyframes(src)
	return ok
}

// ---- playlist and segments ---------------------------------------------------

// Playlist returns main.m3u8 for a rendition (building the keyframe index
// first if needed).
func (m *Manager) Playlist(ctx context.Context, src Source) (string, error) {
	st, err := m.stream(ctx, src)
	if err != nil {
		return "", err
	}
	return st.plan.Playlist(), nil
}

func (m *Manager) stream(ctx context.Context, src Source) (*stream, error) {
	key := src.key()
	m.mu.Lock()
	if st := m.streams[key]; st != nil {
		st.lastUsed = time.Now()
		m.mu.Unlock()
		return st, nil
	}
	m.mu.Unlock()

	kf, err := m.Keyframes(ctx, src)
	if err != nil {
		return nil, err
	}
	plan := NewPlan(kf, src.StartSec+src.DurationSec, TargetSegmentSec)
	if plan.Len() == 0 {
		return nil, errors.New("no keyframes inside the file's duration")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.streams[key]; st != nil {
		return st, nil
	}
	st := &stream{src: src, dir: filepath.Join(m.dir, key), plan: plan, ready: map[int]string{},
		lastUsed: time.Now(), changed: make(chan struct{})}
	st.loadReady()
	m.streams[key] = st
	return st, nil
}

// loadReady picks up segments finished by earlier jobs (also from before a
// server restart): every job folder lists its finished segments.
func (st *stream) loadReady() {
	dirs, _ := filepath.Glob(filepath.Join(st.dir, "job-*"))
	sort.Strings(dirs)
	for _, d := range dirs {
		for _, n := range readList(filepath.Join(d, "list.txt")) {
			p := segPath(d, n)
			if _, ok := st.ready[n]; !ok && fileExists(p) {
				st.ready[n] = p
			}
		}
	}
}

func (st *stream) broadcast() {
	close(st.changed)
	st.changed = make(chan struct{})
}

func segPath(dir string, n int) string { return filepath.Join(dir, fmt.Sprintf("%05d.ts", n)) }

// readList parses a segment muxer flat list: one finished file per line.
func readList(path string) []int {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []int
	for _, line := range strings.Split(string(data), "\n") {
		name := filepath.Base(strings.TrimSpace(line))
		if n, err := strconv.Atoi(strings.TrimSuffix(name, ".ts")); err == nil && strings.HasSuffix(name, ".ts") {
			out = append(out, n)
		}
	}
	return out
}

// Segment returns the file of segment n, starting or restarting a job as
// needed (a request far from the running job is a seek), and waiting for it.
func (m *Manager) Segment(ctx context.Context, src Source, n int) (string, error) {
	st, err := m.stream(ctx, src)
	if err != nil {
		return "", err
	}
	if n < 0 || n >= st.plan.Len() {
		return "", ErrNotFound
	}
	deadline := time.NewTimer(waitReady)
	defer deadline.Stop()
	restarts := 0
	for {
		m.mu.Lock()
		st.lastUsed = time.Now()
		if p, ok := st.ready[n]; ok {
			m.mu.Unlock()
			return p, nil
		}
		j := st.job
		if j == nil || n < j.start || n > j.last+lookahead || n >= j.stopAt {
			if restarts > 1 {
				m.mu.Unlock()
				return "", fmt.Errorf("segment %d could not be produced", n)
			}
			restarts++
			if j != nil {
				j.cancel()
			}
			j = m.startJob(st, n)
		}
		ch := st.changed
		m.mu.Unlock()

		select {
		case <-ch:
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("segment %d not ready after %s", n, waitReady)
		}
		m.mu.Lock()
		failed := isDone(j.done) && j.err != nil && st.ready[n] == ""
		jerr := j.err
		m.mu.Unlock()
		if failed {
			return "", jerr
		}
	}
}

func isDone(c chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// startJob runs ffmpeg from segment n to the end (or maxTimesPerJob). Caller holds m.mu.
func (m *Manager) startJob(st *stream, n int) *job {
	ctx, cancel := context.WithCancel(m.ctx)
	j := &job{start: n, last: n - 1, stopAt: min(n+maxTimesPerJob, st.plan.Len()), cancel: cancel, done: make(chan struct{}),
		dir: filepath.Join(st.dir, fmt.Sprintf("job-%05d-%x", n, time.Now().UnixNano()&0xffffff))}
	st.job = j
	go m.runJob(ctx, st, j)
	return j
}

// JobArgs builds the ffmpeg command line for a job starting at segment n.
func JobArgs(src Source, plan Plan, n, stopAt int, dir string) []string {
	k := plan.Starts[n]
	a := []string{"-nostdin", "-loglevel", "error"}
	seek := n > 0
	if seek {
		a = append(a, "-ss", ftoa(math.Max(k-src.StartSec-seekMargin, 0)))
	}
	a = append(a, "-copyts", "-i", fsutil.LongPath(src.Path),
		"-map", "0:"+strconv.Itoa(src.VideoIndex), "-map", "0:"+strconv.Itoa(src.AudioIndex), "-c:v", "copy")
	if seek {
		// Stream copy starts at the keyframe the container index lands on,
		// which may be earlier; drop video packets before ours.
		a = append(a, "-bsf:v", `noise=drop=lt(pts*tb\,`+ftoa(k-seekMargin)+`)`)
	}
	a = append(a, AudioArgs(src.Codec, src.Channels)...)
	a = append(a, "-output_ts_offset", strconv.Itoa(TSOffsetSec), "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1",
		"-avoid_negative_ts", "disabled", "-muxdelay", "0",
		"-f", "segment", "-segment_format", "mpegts")
	var times []string
	for i := n + 1; i < plan.Len() && i <= stopAt; i++ {
		times = append(times, ftoa(plan.Starts[i]-k-cutMargin))
	}
	if len(times) > 0 {
		a = append(a, "-segment_times", strings.Join(times, ","))
	}
	return append(a, "-segment_start_number", strconv.Itoa(n),
		"-segment_list_type", "flat", "-segment_list", filepath.Join(dir, "list.txt"),
		filepath.Join(dir, "%05d.ts"))
}

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }

func (m *Manager) runJob(ctx context.Context, st *stream, j *job) {
	err := os.MkdirAll(j.dir, 0o755)
	var stderr limitedBuffer
	var cmd *exec.Cmd
	if err == nil {
		cmd = procutil.Command(ctx, m.ffmpeg, JobArgs(st.src, st.plan, j.start, j.stopAt, j.dir)...)
		cmd.Stderr = &stderr
		err = cmd.Start()
	}
	if err == nil {
		m.log.Debug("hls job started", "path", st.src.Path, "audio", st.src.AudioIndex, "codec", st.src.Codec, "segment", j.start)
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		tick := time.NewTicker(150 * time.Millisecond)
	loop:
		for {
			select {
			case <-tick.C:
				m.collect(st, j)
			case err = <-exited:
				break loop
			}
		}
		tick.Stop()
		m.collect(st, j)
	}

	m.mu.Lock()
	stopped := ctx.Err() != nil
	if stopped {
		err = nil // stopped on purpose (seek, idle, shutdown)
	} else if err != nil {
		err = fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	j.err = err
	if st.job == j {
		st.job = nil
	}
	close(j.done)
	st.broadcast()
	empty := j.last < j.start
	m.mu.Unlock()
	j.cancel()
	if err != nil {
		m.log.Warn("hls job failed", "path", st.src.Path, "segment", j.start, "err", err)
	} else {
		m.log.Debug("hls job ended", "path", st.src.Path, "from", j.start, "to", j.last, "stopped", stopped)
	}
	if empty {
		os.RemoveAll(j.dir)
	}
}

// collect marks segments the job finished as ready. A job stops early when it
// reaches segments another job already made, or the end of its boundary list.
func (m *Manager) collect(st *stream, j *job) {
	done := readList(filepath.Join(j.dir, "list.txt"))
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := false
	for _, n := range done {
		if n <= j.last || n >= j.stopAt {
			continue
		}
		j.last = n
		if _, ok := st.ready[n]; !ok {
			st.ready[n] = segPath(j.dir, n)
			changed = true
		}
	}
	if changed {
		st.broadcast()
	}
	if j.last >= j.start {
		if _, ok := st.ready[j.last+1]; ok || j.last+1 >= j.stopAt {
			j.cancel()
		}
	}
}

// limitedBuffer keeps the first 4 KB of ffmpeg's error output.
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := 4096 - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// ---- background work ---------------------------------------------------------

// Kick asks the background indexer to look for files to index.
func (m *Manager) Kick() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Run stops idle jobs, deletes old segment folders, and builds keyframe
// indexes in the background (one at a time, low priority), until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.kick:
				m.indexPending(ctx)
			}
		}
	}()
	defer wg.Wait()

	janitor := time.NewTicker(30 * time.Second)
	defer janitor.Stop()
	lastSweep := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-janitor.C:
			m.stopIdle(time.Now())
			if time.Since(lastSweep) > 10*time.Minute {
				m.sweep(time.Now())
				lastSweep = time.Now()
			}
		}
	}
}

// LikelyConverted reports whether some device will probably need a file's
// audio converted: any track other than codecs every Android TV decodes. Such
// files get their keyframe index built ahead of time.
func LikelyConverted(info *probe.Info) bool {
	for _, a := range info.Audio {
		switch a.Codec {
		case "aac", "mp3", "opus", "flac", "vorbis":
		default:
			if !strings.HasPrefix(a.Codec, "pcm_") {
				return true
			}
		}
	}
	return false
}

func (m *Manager) stopIdle(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, st := range m.streams {
		idle := now.Sub(st.lastUsed)
		if st.job != nil && idle > idleKill {
			st.job.cancel()
		}
		if st.job == nil && idle > idleDelete {
			delete(m.streams, key)
		}
	}
}

// sweep deletes segment folders unused for idleDelete and keeps the cache
// under cacheCap (least recently used first). Folders of active streams stay.
func (m *Manager) sweep(now time.Time) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	type folder struct {
		path string
		used time.Time
		size int64
	}
	var folders []folder
	var total int64
	m.mu.Lock()
	active := map[string]time.Time{}
	for key, st := range m.streams {
		active[key] = st.lastUsed
	}
	m.mu.Unlock()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(m.dir, e.Name())
		used, isActive := active[e.Name()]
		if !isActive {
			if fi, err := e.Info(); err == nil {
				used = fi.ModTime()
			}
		}
		f := folder{path: p, used: used, size: dirSize(p)}
		total += f.size
		if !isActive && now.Sub(used) > idleDelete {
			os.RemoveAll(p)
			total -= f.size
			continue
		}
		if !isActive {
			folders = append(folders, f)
		}
	}
	sort.Slice(folders, func(i, j int) bool { return folders[i].used.Before(folders[j].used) })
	for _, f := range folders {
		if total <= cacheCap {
			break
		}
		os.RemoveAll(f.path)
		total -= f.size
	}
}

func (m *Manager) indexPending(ctx context.Context) {
	if m.Pending == nil {
		return
	}
	list, err := m.Pending(ctx)
	if err != nil {
		return
	}
	for i, src := range list {
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		m.indexing, m.pending = src.Path, len(list)-i
		m.mu.Unlock()
		if _, err := m.keyframes(ctx, src, true); err == nil && m.AfterIndex != nil {
			m.AfterIndex(ctx, src.ID)
		}
	}
	m.mu.Lock()
	m.indexing, m.pending = "", 0
	m.mu.Unlock()
}

// Status is shown in /api/library/status.
type Status struct {
	Jobs         int    `json:"jobs"`
	Streams      int    `json:"streams"`
	IndexPending int    `json:"keyframeIndexPending"`
	Indexing     string `json:"keyframeIndexing,omitempty"`
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Status{Streams: len(m.streams), IndexPending: m.pending, Indexing: m.indexing}
	for _, st := range m.streams {
		if st.job != nil {
			s.Jobs++
		}
	}
	return s
}

// ---- helpers -----------------------------------------------------------------

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

func writeJSONAtomic(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
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
