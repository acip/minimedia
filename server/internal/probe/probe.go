// Package probe runs ffprobe and derives video/audio/subtitle/chapter info
// from its JSON output (PLAN.md section 3.3).
package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/acip/minimedia/server/internal/procutil"
	"github.com/acip/minimedia/server/internal/subs"
)

// Run executes ffprobe and returns its raw JSON.
func Run(ctx context.Context, ffprobe, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := procutil.Command(ctx, ffprobe, "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", "-show_chapters", path)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("ffprobe: %v: %s", err, msg)
	}
	return out, nil
}

// Raw mirrors the parts of ffprobe's JSON we use.
type Raw struct {
	Streams  []RawStream  `json:"streams"`
	Chapters []RawChapter `json:"chapters"`
	Format   struct {
		FormatName string            `json:"format_name"`
		Duration   string            `json:"duration"`
		StartTime  string            `json:"start_time"`
		Size       string            `json:"size"`
		BitRate    string            `json:"bit_rate"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
}

type RawStream struct {
	Index          int               `json:"index"`
	CodecType      string            `json:"codec_type"`
	CodecName      string            `json:"codec_name"`
	Profile        string            `json:"profile"`
	CodecTagString string            `json:"codec_tag_string"`
	Width          int               `json:"width"`
	Height         int               `json:"height"`
	SampleAspect   string            `json:"sample_aspect_ratio"`
	DisplayAspect  string            `json:"display_aspect_ratio"`
	PixFmt         string            `json:"pix_fmt"`
	BitsPerRaw     string            `json:"bits_per_raw_sample"`
	ColorTransfer  string            `json:"color_transfer"`
	ColorPrimaries string            `json:"color_primaries"`
	RFrameRate     string            `json:"r_frame_rate"`
	AvgFrameRate   string            `json:"avg_frame_rate"`
	Channels       int               `json:"channels"`
	ChannelLayout  string            `json:"channel_layout"`
	Tags           map[string]string `json:"tags"`
	Disposition    map[string]int    `json:"disposition"`
	SideDataList   []map[string]any  `json:"side_data_list"`
}

type RawChapter struct {
	StartTime string            `json:"start_time"`
	EndTime   string            `json:"end_time"`
	Tags      map[string]string `json:"tags"`
}

// Video describes the primary video stream.
type Video struct {
	Index     int     `json:"index"`
	Codec     string  `json:"codec"`
	Profile   string  `json:"profile,omitempty"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	FPS       float64 `json:"fps"`
	BitDepth  int     `json:"bitDepth"`
	HDR       string  `json:"hdr"` // sdr | hdr10 | hlg | dv
	DVProfile int     `json:"dvProfile,omitempty"`
	// DisplayAspect is width/height after applying the sample aspect ratio.
	DisplayAspect float64 `json:"displayAspect"`
}

type Audio struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec"`
	Channels int    `json:"channels"`
	Layout   string `json:"layout,omitempty"`
	Language string `json:"language"`
	Title    string `json:"title,omitempty"`
	Default  bool   `json:"default"`
}

type Subtitle struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec"`
	Language string `json:"language"`
	Title    string `json:"title,omitempty"`
	Forced   bool   `json:"forced"`
	Default  bool   `json:"default"`
	SDH      bool   `json:"sdh"`
}

type Chapter struct {
	StartMs int64
	EndMs   int64
	Title   string
}

// Marker is an intro/recap/credits range derived from chapters.
type Marker struct {
	Kind    string `json:"kind"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
}

// Info is everything derived from one probe.
type Info struct {
	Container  string
	DurationMs int64
	StartMs    int64 // container start time (non-zero e.g. for MPEG-TS recordings)
	BitRate    int64
	Video      *Video
	Audio      []Audio
	Subtitles  []Subtitle
	Chapters   []Chapter
	Markers    []Marker
}

// Parse decodes ffprobe JSON and derives Info.
func Parse(data []byte) (*Info, error) {
	var raw Raw
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse ffprobe json: %w", err)
	}
	info := &Info{
		Container:  raw.Format.FormatName,
		DurationMs: secondsToMs(raw.Format.Duration),
		StartMs:    secondsToMs(raw.Format.StartTime),
	}
	info.BitRate, _ = strconv.ParseInt(raw.Format.BitRate, 10, 64)
	for _, s := range raw.Streams {
		switch s.CodecType {
		case "video":
			if s.Disposition["attached_pic"] == 1 || info.Video != nil {
				continue // cover art / secondary video
			}
			info.Video = deriveVideo(s)
		case "audio":
			info.Audio = append(info.Audio, Audio{
				Index:    s.Index,
				Codec:    s.CodecName,
				Channels: s.Channels,
				Layout:   s.ChannelLayout,
				Language: lang(s.Tags),
				Title:    tag(s.Tags, "title"),
				Default:  s.Disposition["default"] == 1,
			})
		case "subtitle":
			title := tag(s.Tags, "title")
			lt := strings.ToLower(title)
			info.Subtitles = append(info.Subtitles, Subtitle{
				Index:    s.Index,
				Codec:    s.CodecName,
				Language: lang(s.Tags),
				Title:    title,
				Forced:   s.Disposition["forced"] == 1 || strings.Contains(lt, "forced"),
				Default:  s.Disposition["default"] == 1,
				SDH:      s.Disposition["hearing_impaired"] == 1 || strings.Contains(lt, "sdh"),
			})
		}
	}
	for _, c := range raw.Chapters {
		info.Chapters = append(info.Chapters, Chapter{
			StartMs: secondsToMs(c.StartTime),
			EndMs:   secondsToMs(c.EndTime),
			Title:   tag(c.Tags, "title"),
		})
	}
	info.Markers = Markers(info.Chapters)
	return info, nil
}

func deriveVideo(s RawStream) *Video {
	v := &Video{
		Index:   s.Index,
		Codec:   s.CodecName,
		Profile: s.Profile,
		Width:   s.Width,
		Height:  s.Height,
		FPS:     rate(s.AvgFrameRate),
	}
	if v.FPS == 0 {
		v.FPS = rate(s.RFrameRate)
	}
	v.FPS = math.Round(v.FPS*1000) / 1000
	if n, err := strconv.Atoi(s.BitsPerRaw); err == nil && n > 0 {
		v.BitDepth = n
	} else {
		v.BitDepth = 8
		for _, d := range []string{"16", "12", "10"} {
			if strings.Contains(s.PixFmt, "p"+d) {
				v.BitDepth = atoiOr(d, 8)
				break
			}
		}
	}
	v.DisplayAspect = displayAspect(s)

	v.HDR = "sdr"
	switch s.ColorTransfer {
	case "smpte2084":
		v.HDR = "hdr10"
	case "arib-std-b67":
		v.HDR = "hlg"
	}
	tagStr := strings.ToLower(s.CodecTagString)
	if tagStr == "dvhe" || tagStr == "dvh1" || tagStr == "dav1" || tagStr == "dva1" || tagStr == "dvav" {
		v.HDR = "dv"
	}
	for _, sd := range s.SideDataList {
		if t, _ := sd["side_data_type"].(string); strings.Contains(strings.ToLower(t), "dovi") ||
			strings.Contains(strings.ToLower(t), "dolby vision") {
			v.HDR = "dv"
			if p, ok := sd["dv_profile"].(float64); ok {
				v.DVProfile = int(p)
			}
		}
	}
	return v
}

func displayAspect(s RawStream) float64 {
	if s.Width == 0 || s.Height == 0 {
		return 0
	}
	ar := float64(s.Width) / float64(s.Height)
	if sar := ratio(s.SampleAspect); sar > 0 {
		ar *= sar
	}
	return math.Round(ar*10000) / 10000
}

// Markers classifies chapters by title (case-insensitive exact match).
func Markers(chapters []Chapter) []Marker {
	kinds := map[string]string{
		"intro": "intro", "opening": "intro", "op": "intro", "opening credits": "intro", "title sequence": "intro",
		"recap": "recap", "previously": "recap", "previously on": "recap",
		"credits": "credits", "ending": "credits", "ed": "credits", "end credits": "credits", "outro": "credits",
	}
	var out []Marker
	for _, c := range chapters {
		t := strings.ToLower(strings.TrimSpace(strings.TrimRight(c.Title, ".:…")))
		if k, ok := kinds[t]; ok && c.EndMs > c.StartMs {
			out = append(out, Marker{Kind: k, StartMs: c.StartMs, EndMs: c.EndMs})
		}
	}
	return out
}

// CreditsStartMs returns where the "next episode" prompt starts: the credits
// marker, or max(duration − 30 s, 90% of duration) (PLAN.md 3.6).
func CreditsStartMs(markers []Marker, durationMs int64) (ms int64, fromMarker bool) {
	for _, m := range markers {
		if m.Kind == "credits" {
			return m.StartMs, true
		}
	}
	a := durationMs - 30000
	b := durationMs * 9 / 10
	if a > b {
		return a, false
	}
	return b, false
}

// AudioLabel builds "English 5.1 (E-AC3)".
func AudioLabel(a Audio) string {
	l := subs.LangName(a.Language)
	if a.Language == "und" && a.Title != "" {
		l = a.Title
	}
	return fmt.Sprintf("%s %s (%s)", l, ChannelName(a.Channels, a.Layout), CodecName(a.Codec))
}

func ChannelName(ch int, layout string) string {
	switch {
	case strings.Contains(layout, "7.1") || ch == 8:
		return "7.1"
	case strings.Contains(layout, "5.1") || ch == 6:
		return "5.1"
	case ch == 1:
		return "Mono"
	case ch == 2:
		return "Stereo"
	case ch > 0:
		return fmt.Sprintf("%dch", ch)
	}
	return ""
}

func CodecName(c string) string {
	switch c {
	case "aac":
		return "AAC"
	case "ac3":
		return "AC3"
	case "eac3":
		return "E-AC3"
	case "dts":
		return "DTS"
	case "truehd":
		return "TrueHD"
	case "flac":
		return "FLAC"
	case "opus":
		return "Opus"
	case "mp3":
		return "MP3"
	case "vorbis":
		return "Vorbis"
	}
	return strings.ToUpper(c)
}

func lang(tags map[string]string) string {
	l := tag(tags, "language")
	if l == "" {
		return "und"
	}
	return subs.NormalizeLang(l)
}

func tag(tags map[string]string, k string) string {
	for key, v := range tags {
		if strings.EqualFold(key, k) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func secondsToMs(s string) int64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0
	}
	return int64(math.Round(f * 1000))
}

func rate(s string) float64 {
	return ratio(strings.Replace(s, "/", ":", 1))
}

func ratio(s string) float64 {
	a, b, ok := strings.Cut(s, ":")
	if !ok {
		return 0
	}
	n, err1 := strconv.ParseFloat(a, 64)
	d, err2 := strconv.ParseFloat(b, 64)
	if err1 != nil || err2 != nil || d == 0 || n == 0 {
		return 0
	}
	return n / d
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
