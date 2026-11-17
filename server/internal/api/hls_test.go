package api

import (
	"context"
	"strings"
	"testing"

	"github.com/acip/minimedia/server/internal/hls"
	"github.com/acip/minimedia/server/internal/store"
)

// With a converter, unplayable audio gets an HLS URL; embedded text subtitles
// become extracted files and image subtitles are marked unavailable.
func TestPlaybackHLS(t *testing.T) {
	e := setup(t)
	id := e.probeMovie()
	e.api.opt.HLS = hls.New(context.Background(), e.dataDir, "ffmpeg", "ffprobe", e.api.log)
	tx, _ := e.api.store.Begin(context.Background())
	if err := tx.ReplaceSubtitles(id, "embedded", []store.Subtitle{
		{ID: "srt1", Source: "embedded", StreamIndex: 4, Language: "en", Codec: "subrip"},
		{ID: "pgs1", Source: "embedded", StreamIndex: 5, Language: "ro", Codec: "hdmv_pgs_subtitle"},
	}); err != nil {
		t.Fatal(err)
	}
	tx.Commit()

	// The DTS track on a device that only passes AC3 through: AC3 5.1.
	c := map[string]any{"videoCodecs": []string{"h264"}, "audioDecoders": []string{"aac"},
		"audioPassthrough": []string{"ac3"}, "audioTrack": 1}
	var p struct {
		playbackResp
		ConvertedAudio *string `json:"convertedAudio"`
		Subtitles      []struct {
			ID        string  `json:"id"`
			Label     string  `json:"label"`
			Embedded  bool    `json:"embedded"`
			URL       *string `json:"url"`
			Available bool    `json:"available"`
			Selected  bool    `json:"selected"`
		} `json:"subtitles"`
	}
	e.json("POST", "/api/items/"+id+"/playback", c, 200, &p)
	if p.Mode != "hls" || p.URL != "/stream/"+id+"/hls/1/ac3/main.m3u8" || p.Container != "ts" ||
		p.ConvertedAudio == nil || *p.ConvertedAudio != "ac3" || len(p.Warnings) != 0 {
		t.Errorf("decision = %+v", p.playbackResp)
	}
	subs := map[string]int{}
	for i, s := range p.Subtitles {
		subs[s.ID] = i
	}
	srt := p.Subtitles[subs["srt1"]]
	if !srt.Embedded || srt.URL == nil || *srt.URL != "/subs/"+id+"/srt1.srt" || !srt.Available {
		t.Errorf("text subtitle = %+v", srt)
	}
	pgs := p.Subtitles[subs["pgs1"]]
	if pgs.Available || pgs.URL != nil || pgs.Selected || !strings.Contains(pgs.Label, "unavailable") {
		t.Errorf("image subtitle = %+v", pgs)
	}

	// A playable choice stays direct; after a failed direct play, AAC.
	c["audioTrack"] = 3
	e.json("POST", "/api/items/"+id+"/playback", c, 200, &p)
	if p.Mode != "direct" {
		t.Errorf("aac track: %+v", p.playbackResp)
	}
	c["forceConvert"] = true
	e.json("POST", "/api/items/"+id+"/playback", c, 200, &p)
	if p.Mode != "hls" || p.URL != "/stream/"+id+"/hls/3/aac/main.m3u8" {
		t.Errorf("forceConvert: %+v", p.playbackResp)
	}

	// Bad rendition paths.
	e.json("GET", "/stream/"+id+"/hls/9/ac3/main.m3u8", nil, 404, nil)
	e.json("GET", "/stream/"+id+"/hls/1/mp3/main.m3u8", nil, 404, nil)
	e.json("GET", "/stream/"+id+"/hls/1/ac3/seg/x.ts", nil, 404, nil)
}
