// Package config locates, loads and validates config.yaml (PLAN.md section 2).
package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/acip/minimedia/server/internal/procutil"
	"github.com/acip/minimedia/server/internal/subs"
)

// FileName is the config file name looked up next to the executable.
const FileName = "config.yaml"

//go:embed default.yaml
var defaultYAML []byte

// DefaultYAML returns the fully commented default config file.
func DefaultYAML() []byte { return bytes.Clone(defaultYAML) }

type Library struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"` // movies | shows
	Path string `yaml:"path"`
}

type Subtitles struct {
	PreferredLanguages []string `yaml:"preferred_languages"`
	FallbackEncoding   string   `yaml:"fallback_encoding"`
}

type Playback struct {
	AudioConvert string `yaml:"audio_convert"` // auto | always | never
}

type Trickplay struct {
	Enabled         bool `yaml:"enabled"`
	IntervalSeconds int  `yaml:"interval_seconds"`
	Width           int  `yaml:"width"`
	MaxParallelJobs int  `yaml:"max_parallel_jobs"`
}

type Scan struct {
	WatchFolders     bool `yaml:"watch_folders"`
	RescanEveryHours int  `yaml:"rescan_every_hours"`
}

type Config struct {
	Name             string    `yaml:"name"`
	Port             int       `yaml:"port"`
	Discovery        bool      `yaml:"discovery"`
	Libraries        []Library `yaml:"libraries"`
	DataDir          string    `yaml:"data_dir"`
	TMDBAPIKey       string    `yaml:"tmdb_api_key"`
	MetadataLanguage string    `yaml:"metadata_language"`
	Subtitles        Subtitles `yaml:"subtitles"`
	Playback         Playback  `yaml:"playback"`
	Trickplay        Trickplay `yaml:"trickplay"`
	Scan             Scan      `yaml:"scan"`
	FFmpeg           string    `yaml:"ffmpeg"`
	FFprobe          string    `yaml:"ffprobe"`
	LogLevel         string    `yaml:"log_level"`
	LogFile          string    `yaml:"log_file"`

	// Resolved during validation; not part of the file.
	Path        string `yaml:"-"` // the config file that was loaded
	FFmpegPath  string `yaml:"-"`
	FFprobePath string `yaml:"-"`
}

// Defaults returns the configuration used for keys missing from the file.
func Defaults() Config {
	return Config{
		Name:             "MiniMedia",
		Port:             8096,
		Discovery:        true,
		MetadataLanguage: "en-US",
		Subtitles:        Subtitles{PreferredLanguages: []string{"en"}, FallbackEncoding: "windows-1250"},
		Playback:         Playback{AudioConvert: "auto"},
		Trickplay:        Trickplay{Enabled: true, IntervalSeconds: 10, Width: 320, MaxParallelJobs: 1},
		Scan:             Scan{WatchFolders: true, RescanEveryHours: 24},
		LogLevel:         "info",
	}
}

// ErrNotFound is returned by Locate when no config file exists; a default one
// has then been written to DefaultPath.
var ErrNotFound = errors.New("no config file found")

// Candidates returns the config locations in priority order.
func Candidates() []string {
	var out []string
	if exe, err := executableDir(); err == nil {
		out = append(out, filepath.Join(exe, FileName))
	}
	switch runtime.GOOS {
	case "windows":
		if pd := os.Getenv("ProgramData"); pd != "" {
			out = append(out, filepath.Join(pd, "MiniMedia", FileName))
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			out = append(out, filepath.Join(home, "Library", "Application Support", "MiniMedia", FileName))
		}
	default:
		out = append(out, filepath.Join("/etc", "minimedia", FileName))
	}
	return out
}

// Locate returns the first existing config file. If none exists it writes the
// default config next to the executable and returns its path with ErrNotFound.
func Locate() (string, error) {
	cands := Candidates()
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	if len(cands) == 0 {
		return "", fmt.Errorf("cannot determine executable location")
	}
	target := cands[0]
	if err := os.WriteFile(target, defaultYAML, 0o644); err != nil {
		return target, fmt.Errorf("%w; writing a default config to %s failed: %v", ErrNotFound, target, err)
	}
	return target, ErrNotFound
}

// Load parses and validates a config file. All problems — unknown keys
// (named with their line) and invalid values — are returned together as *Errors.
func Load(path string, opt ValidateOptions) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, err := Parse(f)
	var keyErrs *Errors
	if err != nil && !errors.As(err, &keyErrs) {
		return nil, err // YAML syntax error: nothing to validate
	}
	cfg.Path = path
	all := &Errors{}
	if keyErrs != nil {
		all.List = append(all.List, keyErrs.List...)
	}
	var valErrs *Errors
	if err := cfg.Validate(opt); errors.As(err, &valErrs) {
		all.List = append(all.List, valErrs.List...)
	}
	if len(all.List) > 0 {
		return nil, all
	}
	return cfg, nil
}

// Parse decodes YAML on top of Defaults(). Unknown keys are reported as
// *Errors, but the returned config still holds every key that did decode.
func Parse(r io.Reader) (*Config, error) {
	cfg := Defaults()
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		var te *yaml.TypeError
		if errors.As(err, &te) {
			return &cfg, &Errors{List: rewordYAML(te.Errors)}
		}
		return nil, err
	}
	return &cfg, nil
}

// "line 3: field prot not found in type config.Config" -> "line 3: unknown key "prot""
func rewordYAML(errs []string) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		if j := strings.Index(e, ": field "); j >= 0 && strings.Contains(e, " not found in type ") {
			key := e[j+len(": field "):]
			key = key[:strings.Index(key, " not found in type ")]
			out[i] = fmt.Sprintf("%s: unknown key %q", e[:j], key)
			continue
		}
		out[i] = e
	}
	return out
}

// Errors collects every validation problem so they can be printed at once.
type Errors struct{ List []string }

func (e *Errors) Error() string {
	return "invalid configuration:\n  - " + strings.Join(e.List, "\n  - ")
}

func (e *Errors) add(format string, a ...any) { e.List = append(e.List, fmt.Sprintf(format, a...)) }

// ValidateOptions lets tests skip checks that touch the machine.
type ValidateOptions struct {
	SkipPortCheck bool
	SkipFFmpeg    bool
	// portCheckAddrs replaces the addresses the port check binds. Tests use
	// loopback only: binding 0.0.0.0 makes Windows Firewall prompt for every
	// freshly built test binary.
	portCheckAddrs []string
}

// Validate checks the config, resolves data_dir and ffmpeg/ffprobe, and
// returns *Errors listing every problem found.
func (c *Config) Validate(opt ValidateOptions) error {
	errs := &Errors{}
	if strings.TrimSpace(c.Name) == "" {
		c.Name = "MiniMedia"
	}
	if c.Port < 1 || c.Port > 65535 {
		errs.add("port: %d is not a valid port", c.Port)
	} else if !opt.SkipPortCheck {
		// Check IPv4 explicitly: on Windows a dual-stack ":port" listen succeeds
		// as [::]:port even when another program (e.g. Jellyfin) owns 0.0.0.0:port.
		// Loopback is checked too: a program bound only to 127.0.0.1 (e.g. WSL's
		// localhost relay) would receive this machine's own and the Android
		// emulator's connections instead of MiniMedia.
		addrs := opt.portCheckAddrs
		if addrs == nil {
			addrs = []string{"0.0.0.0", "127.0.0.1"}
		}
		for _, addr := range addrs {
			ln, err := net.Listen("tcp4", addr+":"+strconv.Itoa(c.Port))
			if err != nil {
				errs.add("port: %d is already in use by another program (%v)", c.Port, err)
				break
			}
			ln.Close()
		}
	}

	if len(c.Libraries) == 0 {
		errs.add("libraries: at least one library is required (set name, type and path)")
	}
	names := map[string]bool{}
	for i, lib := range c.Libraries {
		where := fmt.Sprintf("libraries[%d]", i)
		if lib.Name != "" {
			where += fmt.Sprintf(" (%s)", lib.Name)
		}
		if strings.TrimSpace(lib.Name) == "" {
			errs.add("%s: name is required", where)
		} else if names[strings.ToLower(lib.Name)] {
			errs.add("%s: duplicate library name", where)
		}
		names[strings.ToLower(lib.Name)] = true
		if lib.Type != "movies" && lib.Type != "shows" {
			errs.add("%s: type must be movies or shows, got %q", where, lib.Type)
		}
		if strings.TrimSpace(lib.Path) == "" {
			errs.add("%s: path is required", where)
			continue
		}
		p := filepath.Clean(lib.Path)
		if !filepath.IsAbs(p) {
			errs.add("%s: path %q must be absolute", where, lib.Path)
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			hint := ""
			if runtime.GOOS == "windows" && len(p) >= 2 && p[1] == ':' {
				hint = " (if this is a mapped network drive, use the UNC path \\\\server\\share instead)"
			}
			errs.add("%s: path %q is not accessible: %v%s", where, lib.Path, err, hint)
			continue
		}
		if !fi.IsDir() {
			errs.add("%s: path %q is not a folder", where, lib.Path)
			continue
		}
		if _, err := os.ReadDir(p); err != nil {
			errs.add("%s: path %q is not readable: %v", where, lib.Path, err)
		}
		c.Libraries[i].Path = p
	}

	if c.DataDir == "" {
		c.DataDir = DefaultDataDir()
	}
	if !filepath.IsAbs(c.DataDir) {
		// Relative data_dir is resolved against the config file's folder.
		c.DataDir = filepath.Join(filepath.Dir(c.Path), c.DataDir)
	}
	c.DataDir = filepath.Clean(c.DataDir)

	c.TMDBAPIKey = strings.TrimSpace(c.TMDBAPIKey)
	if lang, ok := normalizeMetadataLanguage(c.MetadataLanguage); ok {
		c.MetadataLanguage = lang
	} else {
		errs.add("metadata_language: %q must look like en-US, ro-RO or ro", c.MetadataLanguage)
	}

	if len(c.Subtitles.PreferredLanguages) == 0 {
		c.Subtitles.PreferredLanguages = []string{"en"}
	}
	for i, l := range c.Subtitles.PreferredLanguages {
		code, ok := subs.LookupLang(l)
		if !ok {
			errs.add("subtitles.preferred_languages: unknown language %q", l)
			continue
		}
		c.Subtitles.PreferredLanguages[i] = code
	}
	if _, err := subs.LookupEncoding(c.Subtitles.FallbackEncoding); err != nil {
		errs.add("subtitles.fallback_encoding: %v", err)
	}
	switch c.Playback.AudioConvert {
	case "auto", "always", "never":
	default:
		errs.add("playback.audio_convert: must be auto, always or never, got %q", c.Playback.AudioConvert)
	}
	if c.Trickplay.IntervalSeconds < 1 {
		errs.add("trickplay.interval_seconds: must be at least 1")
	}
	if c.Trickplay.Width < 32 || c.Trickplay.Width%2 != 0 {
		errs.add("trickplay.width: must be an even number ≥ 32")
	}
	if c.Trickplay.MaxParallelJobs < 1 {
		errs.add("trickplay.max_parallel_jobs: must be at least 1")
	}
	if c.Scan.RescanEveryHours < 0 {
		errs.add("scan.rescan_every_hours: must be 0 (off) or more")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs.add("log_level: must be debug, info, warn or error, got %q", c.LogLevel)
	}

	if !opt.SkipFFmpeg {
		var err error
		if c.FFmpegPath, err = findTool("ffmpeg", c.FFmpeg); err != nil {
			errs.add("ffmpeg: %v", err)
		}
		if c.FFprobePath, err = findTool("ffprobe", c.FFprobe); err != nil {
			errs.add("ffprobe: %v", err)
		}
	}

	if len(errs.List) > 0 {
		return errs
	}
	return nil
}

// normalizeMetadataLanguage accepts "ro", "ro-RO", "ro_ro" and returns the
// TMDB form ("ro", "ro-RO"). Empty means en-US.
func normalizeMetadataLanguage(s string) (string, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "_", "-"))
	if s == "" {
		return "en-US", true
	}
	lang, region, hasRegion := strings.Cut(s, "-")
	isLetters := func(v string, n int) bool {
		if len(v) != n {
			return false
		}
		for _, r := range v {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
				return false
			}
		}
		return true
	}
	if !isLetters(lang, 2) || (hasRegion && !isLetters(region, 2)) {
		return "", false
	}
	if !hasRegion {
		return strings.ToLower(lang), true
	}
	return strings.ToLower(lang) + "-" + strings.ToUpper(region), true
}

// DefaultDataDir is the OS-specific default for data_dir.
func DefaultDataDir() string {
	switch runtime.GOOS {
	case "windows":
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return filepath.Join(pd, "MiniMedia", "data")
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "MiniMedia", "data")
	default:
		return "/var/lib/minimedia"
	}
}

// findTool resolves ffmpeg/ffprobe: the configured path, then next to the
// executable, then PATH. The result must run "-version" successfully.
func findTool(name, configured string) (string, error) {
	exeName := name
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	var tried []string
	try := func(p string) (string, bool) {
		tried = append(tried, p)
		cmd := exec.Command(p, "-version")
		procutil.Prepare(cmd)
		if out, err := cmd.Output(); err == nil && bytes.Contains(out, []byte("version")) {
			return p, true
		}
		return "", false
	}
	if configured != "" {
		if p, ok := try(configured); ok {
			return p, nil
		}
		return "", fmt.Errorf("configured path %q does not run (%s -version failed)", configured, name)
	}
	if dir, err := executableDir(); err == nil {
		p := filepath.Join(dir, exeName)
		if _, err := os.Stat(p); err == nil {
			if p, ok := try(p); ok {
				return p, nil
			}
		}
	}
	if p, err := exec.LookPath(exeName); err == nil {
		if p, ok := try(p); ok {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found next to the executable or on PATH (tried %s); install a full build, e.g. winget install Gyan.FFmpeg", name, strings.Join(tried, ", "))
}

func executableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe), nil
}
