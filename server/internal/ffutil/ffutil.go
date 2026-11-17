// Package ffutil holds small ffmpeg helpers shared by frame grabs and
// (later) trickplay generation.
package ffutil

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"sync"
	"time"

	"github.com/acip/minimedia/server/internal/procutil"
)

// ToneMap converts HDR (PQ/HLG) frames to SDR BT.709 so thumbnails aren't
// washed out (PLAN.md 5.2). It needs the zscale filter (libzimg).
const ToneMap = "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p"

var (
	filterMu    sync.Mutex
	filterCache = map[string]map[string]bool{}
)

// HasFilter reports whether this ffmpeg build has the named filter. The
// filter list is read once per ffmpeg path.
func HasFilter(ffmpeg, name string) bool {
	filterMu.Lock()
	defer filterMu.Unlock()
	set, ok := filterCache[ffmpeg]
	if !ok {
		set = map[string]bool{}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		out, err := procutil.Command(ctx, ffmpeg, "-hide_banner", "-filters").Output()
		cancel()
		if err == nil {
			set = parseFilters(out)
		}
		filterCache[ffmpeg] = set
	}
	return set[name]
}

// parseFilters reads `ffmpeg -filters` lines like " T.. zscale  V->V  Apply resizing…".
func parseFilters(out []byte) map[string]bool {
	set := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 3 && strings.Contains(f[2], "->") {
			set[f[1]] = true
		}
	}
	return set
}
