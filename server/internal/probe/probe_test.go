package probe

import (
	"os"
	"testing"
)

func load(t *testing.T, name string) *Info {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestEpisodeProbe(t *testing.T) {
	info := load(t, "episode.json")
	if info.DurationMs != 180000 {
		t.Errorf("duration = %d", info.DurationMs)
	}
	v := info.Video
	if v == nil || v.Codec != "h264" || v.Width != 1280 || v.Height != 720 || v.FPS != 24 || v.BitDepth != 8 || v.HDR != "sdr" {
		t.Errorf("video = %+v", v)
	}
	if len(info.Audio) != 1 || info.Audio[0].Codec != "ac3" || info.Audio[0].Language != "en" || info.Audio[0].Channels != 2 {
		t.Errorf("audio = %+v", info.Audio)
	}
	if got := AudioLabel(info.Audio[0]); got != "English Stereo (AC3)" {
		t.Errorf("audio label = %q", got)
	}
	if len(info.Subtitles) != 1 || info.Subtitles[0].Codec != "subrip" || info.Subtitles[0].Language != "en" || info.Subtitles[0].Title != "English" {
		t.Errorf("subtitles = %+v", info.Subtitles)
	}
	want := []Marker{{"intro", 0, 20000}, {"credits", 150000, 180000}}
	if len(info.Markers) != len(want) {
		t.Fatalf("markers = %+v", info.Markers)
	}
	for i := range want {
		if info.Markers[i] != want[i] {
			t.Errorf("marker %d = %+v, want %+v", i, info.Markers[i], want[i])
		}
	}
	if ms, ok := CreditsStartMs(info.Markers, info.DurationMs); ms != 150000 || !ok {
		t.Errorf("credits start = %d %v", ms, ok)
	}
}

func TestHDRProbe(t *testing.T) {
	info := load(t, "hdr.json")
	v := info.Video
	if v == nil || v.Codec != "hevc" || v.BitDepth != 10 || v.HDR != "hdr10" {
		t.Errorf("video = %+v", v)
	}
}

func TestDTSProbe(t *testing.T) {
	info := load(t, "dts.json")
	if len(info.Audio) != 1 || info.Audio[0].Codec != "dts" {
		t.Errorf("audio = %+v", info.Audio)
	}
	if len(info.Markers) != 0 {
		t.Errorf("markers = %+v", info.Markers)
	}
}

func TestDolbyVisionDetection(t *testing.T) {
	data := []byte(`{"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","codec_tag_string":"[0][0][0][0]",
	  "width":3840,"height":2160,"pix_fmt":"yuv420p10le","color_transfer":"smpte2084","avg_frame_rate":"24000/1001",
	  "side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":8,"dv_level":6}]}],
	  "format":{"duration":"10.5"}}`)
	info, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if info.Video.HDR != "dv" || info.Video.DVProfile != 8 || info.Video.FPS != 23.976 || info.DurationMs != 10500 {
		t.Errorf("video = %+v, duration %d", info.Video, info.DurationMs)
	}
	data = []byte(`{"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","codec_tag_string":"dvh1","width":10,"height":10}],"format":{}}`)
	info, _ = Parse(data)
	if info.Video.HDR != "dv" {
		t.Errorf("dvh1 tag not detected: %+v", info.Video)
	}
	data = []byte(`{"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","color_transfer":"arib-std-b67","width":10,"height":10}],"format":{}}`)
	info, _ = Parse(data)
	if info.Video.HDR != "hlg" {
		t.Errorf("hlg not detected: %+v", info.Video)
	}
}

func TestAnamorphicAspect(t *testing.T) {
	data := []byte(`{"streams":[{"index":0,"codec_type":"video","codec_name":"mpeg2video","width":720,"height":576,"sample_aspect_ratio":"64:45"}],"format":{}}`)
	info, _ := Parse(data)
	if a := info.Video.DisplayAspect; a < 1.77 || a > 1.78 {
		t.Errorf("display aspect = %v", a)
	}
}

func TestMarkersFromChapterTitles(t *testing.T) {
	ch := []Chapter{
		{0, 5000, "Previously On"}, {5000, 60000, "OP"}, {60000, 70000, "Opening Credits"},
		{70000, 100000, "Chapter 2"}, {100000, 110000, "End Credits"}, {110000, 120000, "Outro"},
		{120000, 120000, "Credits"}, // zero-length: ignored
	}
	got := Markers(ch)
	want := []string{"recap", "intro", "intro", "credits", "credits"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i].Kind != want[i] {
			t.Errorf("%d: %s, want %s", i, got[i].Kind, want[i])
		}
	}
	if ms, ok := CreditsStartMs(nil, 100*60000); ms != 100*60000-30000 || ok {
		t.Errorf("fallback long = %d", ms)
	}
	if ms, _ := CreditsStartMs(nil, 120000); ms != 108000 {
		t.Errorf("fallback short = %d", ms)
	}
}
