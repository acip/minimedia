// Package playback decides how an item plays on a given device (PLAN.md 4.1):
// direct play, or HLS with the audio converted, plus default audio and
// subtitle tracks. It is pure logic; the API layer supplies the inputs.
package playback

import (
	"strings"

	"github.com/acip/minimedia/server/internal/probe"
)

// Caps is what the TV app reports about the device (PLAN.md 8.5).
type Caps struct {
	VideoCodecs      []string `json:"videoCodecs"`
	HDR              []string `json:"hdr"`
	AudioDecoders    []string `json:"audioDecoders"`
	AudioPassthrough []string `json:"audioPassthrough"`
	MaxChannels      int      `json:"maxChannels"`
	AudioTrack       *int     `json:"audioTrack"`   // chosen audio stream index
	ForceConvert     bool     `json:"forceConvert"` // direct play failed on the device
}

// Prefs are the remembered choices for a show (zero for movies).
type Prefs struct {
	AudioLanguage    string
	SubtitleLanguage string
	SubtitlesMode    string // off | on | forced_only
	Set              bool   // false = never saved: choose automatically
}

// Sub is a subtitle track as far as the decision is concerned.
type Sub struct {
	ID       string
	Language string
	Forced   bool
	SDH      bool
}

type Input struct {
	Info         *probe.Info
	Subs         []Sub
	Caps         Caps
	Prefs        Prefs
	Preferred    []string // subtitles.preferred_languages, in priority order
	AudioConvert string   // auto | always | never
}

type Decision struct {
	Mode              string       // direct | hls
	AudioIndex        int          // stream index of the selected audio track, -1 = none
	Playable          map[int]bool // per audio stream index: plays without conversion
	ConvertTo         string       // eac3 | aac when Mode is hls
	SubtitleID        string       // default subtitle, "" = off
	UnsupportedReason string       // video the device can't decode (no video transcoding in v1)
	Warnings          []string
}

// Assumed when the app sends empty lists (an old or minimal client).
var (
	defaultVideo = []string{"h264", "hevc", "vp9"}
	defaultAudio = []string{"aac", "mp3", "opus", "flac", "vorbis", "ac3", "eac3"}
)

// VideoCodec maps ffprobe names to the names the app reports.
func VideoCodec(c string) string {
	switch c {
	case "mpeg2video":
		return "mpeg2"
	case "h265":
		return "hevc"
	}
	return c
}

// AudioCodec maps ffprobe names to the names the app reports.
func AudioCodec(c string) string {
	switch {
	case strings.HasPrefix(c, "pcm_"):
		return "pcm"
	case c == "dca":
		return "dts"
	case c == "mp3float":
		return "mp3"
	}
	return c
}

func has(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// AudioPlayable reports whether the device plays a track without conversion:
// it decodes the codec (Android downmixes the decoded channels as needed) or
// can pass it through to an HDMI receiver. PCM always plays.
func AudioPlayable(a probe.Audio, caps Caps) bool {
	c := AudioCodec(a.Codec)
	if c == "pcm" {
		return true
	}
	dec := caps.AudioDecoders
	if len(dec) == 0 && len(caps.AudioPassthrough) == 0 {
		dec = defaultAudio
	}
	return has(dec, c) || has(caps.AudioPassthrough, c)
}

func Decide(in Input) Decision {
	d := Decision{Mode: "direct", AudioIndex: -1, Playable: map[int]bool{}}
	info := in.Info
	if info == nil {
		info = &probe.Info{}
	}

	if v := info.Video; v != nil {
		codecs := in.Caps.VideoCodecs
		if len(codecs) == 0 {
			codecs = defaultVideo
		}
		if !has(codecs, VideoCodec(v.Codec)) {
			d.UnsupportedReason = codecName(v.Codec) + " video is not supported by this device"
		}
		if v.HDR == "dv" && v.DVProfile == 5 && !has(in.Caps.HDR, "dv") {
			d.Warnings = append(d.Warnings, "This Dolby Vision (profile 5) video needs a Dolby Vision display; colours will look wrong")
		}
	}

	for _, a := range info.Audio {
		d.Playable[a.Index] = AudioPlayable(a, in.Caps)
	}
	if len(info.Audio) > 0 {
		d.AudioIndex = pickAudio(info.Audio, d.Playable, in)
	}

	if d.AudioIndex >= 0 {
		needConvert := !d.Playable[d.AudioIndex] || in.Caps.ForceConvert || in.AudioConvert == "always"
		switch {
		case needConvert && in.AudioConvert != "never":
			d.Mode = "hls"
			d.ConvertTo = convertTarget(info.Audio, d.AudioIndex, in.Caps)
		case needConvert:
			d.Warnings = append(d.Warnings, "This audio can't be played on this device and audio conversion is turned off (playback.audio_convert: never)")
		}
	}

	audioLang := "und"
	for _, a := range info.Audio {
		if a.Index == d.AudioIndex {
			audioLang = a.Language
		}
	}
	d.SubtitleID = pickSubtitle(in.Subs, audioLang, in)
	return d
}

// convertTarget picks the codec converted audio is encoded to (PLAN.md 4.1):
// surround stays surround as E-AC3 5.1 when the device decodes or passes
// through E-AC3, else as AC3 5.1 (common for TVs that only report AC3 over
// HDMI/ARC), else AAC stereo. Stereo or mono sources become AAC. After a
// failed direct play (forceConvert) the device may have claimed a codec it
// can't really play, so AAC, which every device decodes, is used.
func convertTarget(tracks []probe.Audio, index int, caps Caps) string {
	channels := 0
	for _, a := range tracks {
		if a.Index == index {
			channels = a.Channels
		}
	}
	if caps.ForceConvert || channels <= 2 {
		return "aac"
	}
	supports := func(c string) bool { return has(caps.AudioDecoders, c) || has(caps.AudioPassthrough, c) }
	switch {
	case supports("eac3"):
		return "eac3"
	case supports("ac3"):
		return "ac3"
	}
	return "aac"
}

// pickAudio: an explicit choice; else the show's remembered language, then
// the preferred languages; else the file's default track. Within a language a
// playable, non-commentary track wins (e.g. AC3 over an unplayable TrueHD).
func pickAudio(tracks []probe.Audio, playable map[int]bool, in Input) int {
	if t := in.Caps.AudioTrack; t != nil {
		for _, a := range tracks {
			if a.Index == *t {
				return a.Index
			}
		}
	}
	var langs []string
	if in.Prefs.AudioLanguage != "" {
		langs = append(langs, in.Prefs.AudioLanguage)
	}
	langs = append(langs, in.Preferred...)
	for _, l := range langs {
		var cands []probe.Audio
		for _, a := range tracks {
			if a.Language == l {
				cands = append(cands, a)
			}
		}
		if len(cands) > 0 {
			return best(cands, playable).Index
		}
	}
	def := tracks[0]
	for _, a := range tracks {
		if a.Default {
			def = a
			break
		}
	}
	var same []probe.Audio
	for _, a := range tracks {
		if a.Language == def.Language {
			same = append(same, a)
		}
	}
	return best(same, playable).Index
}

func isCommentary(a probe.Audio) bool {
	t := strings.ToLower(a.Title)
	return strings.Contains(t, "comment") || strings.Contains(t, "descriptive") || strings.Contains(t, "audio description")
}

// best ranks tracks: main before commentary, playable before not, default
// before not, then file order.
func best(cands []probe.Audio, playable map[int]bool) probe.Audio {
	score := func(a probe.Audio) int {
		s := 0
		if !isCommentary(a) {
			s += 4
		}
		if playable[a.Index] {
			s += 2
		}
		if a.Default {
			s++
		}
		return s
	}
	b := cands[0]
	for _, a := range cands[1:] {
		if score(a) > score(b) {
			b = a
		}
	}
	return b
}

// pickSubtitle chooses the default subtitle.
//   - Saved show prefs: off → none; forced_only → forced track in the audio
//     language; on → the saved (or first preferred) language.
//   - Otherwise (automatic): full subtitles in the first preferred language
//     that ranks above the audio language, e.g. Romanian subtitles for English
//     audio with preferred [ro, en], none for Romanian audio. Failing that, a
//     forced track in the audio language (foreign-dialogue parts).
func pickSubtitle(subs []Sub, audioLang string, in Input) string {
	full := func(lang string) string {
		id := ""
		for _, s := range subs {
			if s.Language != lang || s.Forced {
				continue
			}
			if !s.SDH {
				return s.ID
			}
			if id == "" {
				id = s.ID
			}
		}
		return id
	}
	forced := func() string {
		for _, s := range subs {
			if s.Forced && s.Language == audioLang {
				return s.ID
			}
		}
		return ""
	}
	if in.Prefs.Set {
		switch in.Prefs.SubtitlesMode {
		case "off":
			return ""
		case "forced_only":
			return forced()
		case "on":
			langs := append([]string{}, in.Preferred...)
			if in.Prefs.SubtitleLanguage != "" {
				langs = append([]string{in.Prefs.SubtitleLanguage}, langs...)
			}
			for _, l := range langs {
				if id := full(l); id != "" {
					return id
				}
			}
			return forced()
		}
	}
	rank := len(in.Preferred)
	for i, l := range in.Preferred {
		if l == audioLang {
			rank = i
			break
		}
	}
	for _, l := range in.Preferred[:rank] {
		if id := full(l); id != "" {
			return id
		}
	}
	return forced()
}

func codecName(c string) string {
	switch c {
	case "av1":
		return "AV1"
	case "hevc":
		return "HEVC"
	case "h264":
		return "H.264"
	case "vp9":
		return "VP9"
	case "mpeg2video":
		return "MPEG-2"
	case "vc1":
		return "VC-1"
	}
	return strings.ToUpper(c)
}
