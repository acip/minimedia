package playback

import (
	"strings"
	"testing"

	"github.com/acip/minimedia/server/internal/probe"
)

var tvCaps = Caps{
	VideoCodecs:      []string{"h264", "hevc", "vp9"},
	HDR:              []string{"hdr10", "hlg"},
	AudioDecoders:    []string{"aac", "mp3", "opus", "flac", "vorbis", "ac3", "eac3"},
	AudioPassthrough: []string{},
	MaxChannels:      6,
}

func info(video string, audio ...probe.Audio) *probe.Info {
	return &probe.Info{Video: &probe.Video{Index: 0, Codec: video, HDR: "sdr"}, Audio: audio}
}

func aud(idx int, codec, lang string, def bool, title ...string) probe.Audio {
	a := probe.Audio{Index: idx, Codec: codec, Language: lang, Channels: 6, Default: def}
	if len(title) > 0 {
		a.Title = title[0]
	}
	return a
}

func TestDirectPlayWhenEverythingPlays(t *testing.T) {
	d := Decide(Input{Info: info("h264", aud(1, "aac", "eng", true)), Caps: tvCaps, Preferred: []string{"en"}, AudioConvert: "auto"})
	if d.Mode != "direct" || d.AudioIndex != 1 || !d.Playable[1] || d.UnsupportedReason != "" || len(d.Warnings) != 0 {
		t.Errorf("%+v", d)
	}
}

func TestUnsupportedVideo(t *testing.T) {
	d := Decide(Input{Info: info("av1", aud(1, "opus", "eng", true)), Caps: tvCaps, AudioConvert: "auto"})
	if !strings.Contains(d.UnsupportedReason, "AV1") {
		t.Errorf("reason = %q", d.UnsupportedReason)
	}
	// A device that reports AV1 plays it.
	c := tvCaps
	c.VideoCodecs = append([]string{"av1"}, c.VideoCodecs...)
	if d := Decide(Input{Info: info("av1"), Caps: c}); d.UnsupportedReason != "" {
		t.Errorf("av1-capable device: %q", d.UnsupportedReason)
	}
}

func TestUnplayableAudioNeedsConversion(t *testing.T) {
	in := Input{Info: info("h264", aud(1, "dts", "eng", true)), Caps: tvCaps, AudioConvert: "auto"}
	d := Decide(in)
	if d.Mode != "hls" || d.ConvertTo != "eac3" || d.Playable[1] {
		t.Errorf("dts on an eac3 device: %+v", d)
	}
	// No E-AC3 but AC3 (e.g. a Chromecast whose TV reports only AC3) → AC3 5.1.
	in.Caps.AudioDecoders = []string{"aac", "mp3"}
	in.Caps.AudioPassthrough = []string{"ac3"}
	if d := Decide(in); d.ConvertTo != "ac3" {
		t.Errorf("convert to %q, want ac3", d.ConvertTo)
	}
	// Neither → AAC stereo; a stereo source is always AAC.
	in.Caps.AudioPassthrough = nil
	if d := Decide(in); d.ConvertTo != "aac" {
		t.Errorf("convert to %q, want aac", d.ConvertTo)
	}
	stereo := in
	stereo.Info = info("h264", probe.Audio{Index: 1, Codec: "dts", Language: "eng", Channels: 2, Default: true})
	stereo.Caps = tvCaps
	if d := Decide(stereo); d.ConvertTo != "aac" {
		t.Errorf("stereo source: convert to %q, want aac", d.ConvertTo)
	}
	// After a failed direct play, AAC regardless of what the device claims.
	forced := Input{Info: info("h264", aud(1, "eac3", "eng", true)), Caps: tvCaps, AudioConvert: "auto"}
	forced.Caps.ForceConvert = true
	if d := Decide(forced); d.Mode != "hls" || d.ConvertTo != "aac" {
		t.Errorf("forceConvert: %+v", d)
	}
	// Passthrough to a receiver plays DTS directly.
	in.Caps.AudioPassthrough = []string{"ac3", "eac3", "dts"}
	if d := Decide(in); d.Mode != "direct" || !d.Playable[1] {
		t.Errorf("dts passthrough: %+v", d)
	}
	// audio_convert: never → direct with a warning.
	in.Caps.AudioPassthrough = nil
	in.AudioConvert = "never"
	if d := Decide(in); d.Mode != "direct" || len(d.Warnings) != 1 {
		t.Errorf("never: %+v", d)
	}
	// always / forceConvert → hls even for playable audio.
	ok := Input{Info: info("h264", aud(1, "aac", "eng", true)), Caps: tvCaps, AudioConvert: "always"}
	if d := Decide(ok); d.Mode != "hls" {
		t.Errorf("always: %+v", d)
	}
	ok.AudioConvert = "auto"
	ok.Caps.ForceConvert = true
	if d := Decide(ok); d.Mode != "hls" {
		t.Errorf("forceConvert: %+v", d)
	}
}

func TestPCMAndCodecNames(t *testing.T) {
	d := Decide(Input{Info: info("mpeg2video", aud(1, "pcm_s24le", "eng", true)), Caps: Caps{VideoCodecs: []string{"mpeg2"}, AudioDecoders: []string{"aac"}}})
	if d.Mode != "direct" || d.UnsupportedReason != "" {
		t.Errorf("%+v", d)
	}
	// Empty capability lists (minimal client) assume a typical Android TV.
	d = Decide(Input{Info: info("hevc", aud(1, "eac3", "eng", true), aud(2, "truehd", "eng", false))})
	if !d.Playable[1] || d.Playable[2] || d.UnsupportedReason != "" {
		t.Errorf("defaults: %+v", d)
	}
}

func TestAudioSelection(t *testing.T) {
	tracks := []probe.Audio{
		aud(1, "truehd", "eng", true),
		aud(2, "ac3", "eng", false, "Director's Commentary"),
		aud(3, "ac3", "eng", false),
		aud(4, "aac", "ron", false),
	}
	for i := range tracks {
		tracks[i].Language = map[string]string{"eng": "en", "ron": "ro"}[tracks[i].Language]
	}
	base := Input{Info: info("h264", tracks...), Caps: tvCaps, AudioConvert: "auto"}

	in := base
	in.Preferred = []string{"en"}
	if d := Decide(in); d.AudioIndex != 3 || d.Mode != "direct" {
		t.Errorf("playable English main track over TrueHD and commentary: got %d (%s)", d.AudioIndex, d.Mode)
	}
	in.Preferred = []string{"ro", "en"}
	if d := Decide(in); d.AudioIndex != 4 {
		t.Errorf("preferred Romanian: got %d", d.AudioIndex)
	}
	in.Prefs = Prefs{AudioLanguage: "en", Set: true}
	if d := Decide(in); d.AudioIndex != 3 {
		t.Errorf("show prefs beat preferred languages: got %d", d.AudioIndex)
	}
	in = base
	in.Preferred = []string{"fr"}
	if d := Decide(in); d.AudioIndex != 3 {
		t.Errorf("no language match → default track's language, playable: got %d", d.AudioIndex)
	}
	idx := 1
	in.Caps.AudioTrack = &idx
	if d := Decide(in); d.AudioIndex != 1 || d.Mode != "hls" {
		t.Errorf("explicit choice is kept even if it needs conversion: %+v", d)
	}
}

func TestSubtitleSelection(t *testing.T) {
	subs := []Sub{
		{ID: "en-sdh", Language: "en", SDH: true},
		{ID: "en", Language: "en"},
		{ID: "en-forced", Language: "en", Forced: true},
		{ID: "ro", Language: "ro"},
	}
	eng := info("h264", aud(1, "aac", "en", true))
	ron := info("h264", aud(1, "aac", "ro", true))
	cases := []struct {
		name  string
		info  *probe.Info
		pref  []string
		prefs Prefs
		want  string
	}{
		{"ro speaker, English film", eng, []string{"ro", "en"}, Prefs{}, "ro"},
		{"ro speaker, Romanian film", ron, []string{"ro", "en"}, Prefs{}, ""},
		{"en speaker, English film: forced only", eng, []string{"en"}, Prefs{}, "en-forced"},
		{"en speaker, Romanian film", ron, []string{"en"}, Prefs{}, "en"},
		{"saved off", eng, []string{"ro"}, Prefs{SubtitlesMode: "off", Set: true}, ""},
		{"saved forced only", eng, []string{"ro"}, Prefs{SubtitlesMode: "forced_only", Set: true}, "en-forced"},
		{"saved on, English", ron, []string{"ro"}, Prefs{SubtitlesMode: "on", SubtitleLanguage: "en", Set: true}, "en"},
		{"saved on, no language", eng, []string{"ro"}, Prefs{SubtitlesMode: "on", Set: true}, "ro"},
	}
	for _, c := range cases {
		d := Decide(Input{Info: c.info, Subs: subs, Caps: tvCaps, Preferred: c.pref, Prefs: c.prefs})
		if d.SubtitleID != c.want {
			t.Errorf("%s: got %q, want %q", c.name, d.SubtitleID, c.want)
		}
	}
	// SDH is used when it's the only full track in the language.
	d := Decide(Input{Info: ron, Subs: []Sub{{ID: "sdh", Language: "en", SDH: true}}, Preferred: []string{"en"}})
	if d.SubtitleID != "sdh" {
		t.Errorf("sdh fallback: %q", d.SubtitleID)
	}
}

func TestDolbyVisionProfile5Warning(t *testing.T) {
	in := info("hevc", aud(1, "eac3", "en", true))
	in.Video.HDR, in.Video.DVProfile = "dv", 5
	if d := Decide(Input{Info: in, Caps: tvCaps}); len(d.Warnings) != 1 {
		t.Errorf("want DV5 warning: %+v", d)
	}
	c := tvCaps
	c.HDR = append(c.HDR, "dv")
	if d := Decide(Input{Info: in, Caps: c}); len(d.Warnings) != 0 {
		t.Errorf("DV display: %+v", d)
	}
}
