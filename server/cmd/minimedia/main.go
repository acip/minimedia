// Command minimedia is the MiniMedia server. It takes no command-line
// arguments: everything comes from config.yaml (see PLAN.md section 2).
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/acip/minimedia/server/internal/api"
	"github.com/acip/minimedia/server/internal/config"
	"github.com/acip/minimedia/server/internal/discovery"
	"github.com/acip/minimedia/server/internal/hls"
	"github.com/acip/minimedia/server/internal/images"
	"github.com/acip/minimedia/server/internal/library"
	"github.com/acip/minimedia/server/internal/logfile"
	"github.com/acip/minimedia/server/internal/metadata"
	"github.com/acip/minimedia/server/internal/power"
	"github.com/acip/minimedia/server/internal/probe"
	"github.com/acip/minimedia/server/internal/procutil"
	"github.com/acip/minimedia/server/internal/store"
	"github.com/acip/minimedia/server/internal/subs"
	"github.com/acip/minimedia/server/internal/trickplay"
	"github.com/acip/minimedia/server/internal/version"
	"github.com/acip/minimedia/server/internal/watch"
)

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) > 1 {
		fmt.Fprintf(os.Stderr, "minimedia %s takes no arguments. Settings are read from the first of:\n", version.Version)
		for _, c := range config.Candidates() {
			fmt.Fprintf(os.Stderr, "  %s\n", c)
		}
		return 2
	}

	path, err := config.Locate()
	if err == config.ErrNotFound {
		fmt.Fprintf(os.Stderr, "No config file was found, so a default one was written to:\n  %s\n"+
			"Edit it to set your media folders under 'libraries', then start MiniMedia again.\n", path)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	cfg, err := config.Load(path, config.ValidateOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
		return 2
	}

	log, closeLog, err := newLogger(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer closeLog()
	log.Info("MiniMedia starting", "version", version.Version, "config", path, "dataDir", cfg.DataDir,
		"ffmpeg", cfg.FFmpegPath, "ffprobe", cfg.FFprobePath)

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Error("cannot create data_dir", "path", cfg.DataDir, "err", err)
		return 1
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "minimedia.db"))
	if err != nil {
		log.Error("cannot open database", "err", err)
		return 1
	}
	defer st.Close()
	serverID, err := st.ServerID()
	if err != nil {
		log.Error("cannot read server id", "err", err)
		return 1
	}
	fallback, _ := subs.LookupEncoding(cfg.Subtitles.FallbackEncoding) // validated above

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := procutil.KillChildrenWithServer(); err != nil {
		log.Warn("ffmpeg processes may outlive the server if it crashes", "err", err)
	}
	go warnPublicNetworks(ctx, log)
	awake := power.New(log)

	var wg sync.WaitGroup
	scanner := library.NewScanner(st, cfg.Libraries, cfg.FFprobePath, log)
	imgs := images.New(st, cfg.DataDir, cfg.FFmpegPath, log)
	thumbs := trickplay.New(st, cfg.DataDir, cfg.FFmpegPath, trickplay.Options{
		Enabled: cfg.Trickplay.Enabled, IntervalSeconds: cfg.Trickplay.IntervalSeconds,
		Width: cfg.Trickplay.Width, MaxParallelJobs: cfg.Trickplay.MaxParallelJobs,
	}, log)
	conv := hls.New(ctx, cfg.DataDir, cfg.FFmpegPath, cfg.FFprobePath, log)
	conv.Pending = func(ctx context.Context) ([]hls.Source, error) {
		items, err := st.ProbedItems(ctx)
		if err != nil {
			return nil, err
		}
		var out []hls.Source
		for _, it := range items {
			info, err := probe.Parse([]byte(it.ProbeJSON))
			if err != nil || info.Video == nil || len(info.Audio) == 0 || !hls.LikelyConverted(info) {
				continue
			}
			if src, err := hls.SourceFor(it.ID, it.Path, it.Size, it.MtimeMs, info, info.Audio[0].Index, "aac"); err == nil {
				out = append(out, src)
			}
		}
		return out, nil
	}
	var fetcher *metadata.Fetcher
	if cfg.TMDBAPIKey != "" {
		client := metadata.NewClient(cfg.TMDBAPIKey, cfg.MetadataLanguage, filepath.Join(cfg.DataDir, "tmdb-cache"))
		fetcher = metadata.NewFetcher(st, client, log)
		log.Info("TMDB metadata enabled", "language", cfg.MetadataLanguage)
		// A new metadata_language re-fetches everything in that language.
		if prev, _ := st.Meta("metadata_language"); prev != cfg.MetadataLanguage {
			counts, _ := st.MetaCounts(ctx)
			if prev != "" || counts.Matched > 0 { // no record but metadata exists: language unknown
				log.Info("metadata language changed; refreshing all metadata", "from", prev, "to", cfg.MetadataLanguage)
				if err := st.ResetMetadata(ctx); err != nil {
					log.Warn("cannot reset metadata", "err", err)
				}
			}
			_ = st.SetMeta("metadata_language", cfg.MetadataLanguage)
		}
	}

	// After scans: metadata first (TMDB artwork beats frame grabs), then frame
	// grabs for whatever still has no backdrop/still. Requests coalesce.
	afterScan := make(chan struct{}, 1)
	scanner.OnChange = func(changed bool) {
		if changed {
			select {
			case afterScan <- struct{}{}:
			default:
			}
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-afterScan:
			}
			if fetcher != nil {
				_ = fetcher.RunOnce(ctx)
			}
			imgs.Warm(ctx)
			thumbs.Kick()
			if cfg.Playback.AudioConvert != "never" {
				conv.Kick()
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		thumbs.Run(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner.Run(ctx, time.Duration(cfg.Scan.RescanEveryHours)*time.Hour)
	}()
	if cfg.Scan.WatchFolders {
		for _, lib := range cfg.Libraries {
			wg.Add(1)
			go func() {
				defer wg.Done()
				watch.Run(ctx, lib.Path, log, func(rel string) { scanner.Changed(lib.Name, rel) })
			}()
		}
		log.Info("watching library folders for changes")
	}

	if cfg.Discovery {
		ds, err := discovery.Listen(":"+strconv.Itoa(discovery.Port), cfg.Name, serverID, version.Version, cfg.Port, log)
		if err != nil {
			log.Warn("discovery disabled", "err", err)
		} else {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ds.Serve(ctx)
			}()
			log.Info("discovery listening", "udp", discovery.Port)
		}
	}

	apiSrv := api.New(api.Options{
		Name: cfg.Name, Version: version.Version, ServerID: serverID, DataDir: cfg.DataDir,
		FallbackEncoding: fallback, PreferredLanguages: cfg.Subtitles.PreferredLanguages,
		AudioConvert: cfg.Playback.AudioConvert, Images: imgs, Metadata: fetcher, Trickplay: thumbs,
		HLS: conv, FFmpeg: cfg.FFmpegPath, Power: awake,
	}, st, scanner, log)
	wg.Add(1)
	go func() {
		defer wg.Done()
		awake.Run(ctx)
	}()
	conv.AfterIndex = apiSrv.WarmSubtitles
	wg.Add(1)
	go func() {
		defer wg.Done()
		conv.Run(ctx)
	}()
	httpSrv := &http.Server{
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: streams run for hours.
	}
	// IPv4 is required (the TV app and discovery use it). IPv6 is a separate,
	// IPv6-only socket: a dual-stack ":port" would bind on Windows even when
	// another program owns the IPv4 port, silently sending clients elsewhere.
	ln4, err := net.Listen("tcp4", "0.0.0.0:"+strconv.Itoa(cfg.Port))
	if err != nil {
		log.Error("cannot listen", "port", cfg.Port, "err", err)
		return 1
	}
	srvErr := make(chan error, 2)
	go func() { srvErr <- httpSrv.Serve(ln4) }()
	if ln6, err := net.Listen("tcp6", "[::]:"+strconv.Itoa(cfg.Port)); err == nil {
		go func() { srvErr <- httpSrv.Serve(ln6) }()
	} else {
		log.Debug("IPv6 listener not available", "err", err)
	}
	log.Info("HTTP listening", "url", fmt.Sprintf("http://localhost:%d", cfg.Port))

	code := 0
	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-srvErr:
		log.Error("HTTP server stopped", "err", err)
		code = 1
	}
	stop()
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shCtx) // open streams are cut after the timeout
	wg.Wait()
	log.Info("stopped")
	return code
}

func newLogger(cfg *config.Config) (*slog.Logger, func(), error) {
	var level slog.Level
	_ = level.UnmarshalText([]byte(cfg.LogLevel))
	var w io.Writer = os.Stderr
	closeFn := func() {}
	if cfg.LogFile != "" {
		// Rotated at 10 MB, 3 old files kept (PLAN.md 7).
		f, err := logfile.Open(cfg.LogFile, logfile.DefaultMaxBytes, logfile.DefaultKeep)
		if err != nil {
			return nil, nil, fmt.Errorf("open log_file: %w", err)
		}
		w = io.MultiWriter(os.Stderr, f)
		closeFn = func() { f.Close() }
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})), closeFn, nil
}
