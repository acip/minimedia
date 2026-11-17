package config

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultYAMLParsesToDefaults(t *testing.T) {
	cfg, err := Parse(bytes.NewReader(DefaultYAML()))
	if err != nil {
		t.Fatalf("default config does not parse: %v", err)
	}
	d := Defaults()
	if cfg.Port != d.Port || cfg.Name != d.Name || !cfg.Discovery || cfg.Subtitles.FallbackEncoding != "windows-1250" ||
		cfg.Trickplay.Width != 320 || cfg.Scan.RescanEveryHours != 24 || cfg.LogLevel != "info" || len(cfg.Libraries) != 0 {
		t.Errorf("default file differs from Defaults(): %+v", cfg)
	}
	err = cfg.Validate(ValidateOptions{SkipPortCheck: true, SkipFFmpeg: true})
	var ve *Errors
	if !errors.As(err, &ve) || !strings.Contains(err.Error(), "at least one library") {
		t.Errorf("default config must fail validation on libraries, got %v", err)
	}
}

func TestLoadReportsUnknownKeysAndInvalidValuesTogether(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	src := "prot: 1\nlibraries:\n  - name: M\n    type: films\n    path: '" + filepath.Join(t.TempDir(), "missing") + "'\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, ValidateOptions{SkipPortCheck: true, SkipFFmpeg: true})
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{`line 1: unknown key "prot"`, `type must be movies or shows`, `is not accessible`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestUnknownKeysNameKeyAndLine(t *testing.T) {
	src := "name: x\nprot: 8096\nsubtitles:\n  fallback_encodin: cp1250\n"
	_, err := Parse(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{`line 2: unknown key "prot"`, `line 4: unknown key "fallback_encodin"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
}

func TestPathsAndPartialOverrides(t *testing.T) {
	dir := t.TempDir()
	movies := filepath.Join(dir, "Filme Ștefan")
	if err := os.Mkdir(movies, 0o755); err != nil {
		t.Fatal(err)
	}
	// Single-quoted backslashes and forward slashes must both work.
	src := "libraries:\n" +
		"  - name: Movies\n    type: movies\n    path: '" + movies + "'\n" +
		"  - name: Movies2\n    type: movies\n    path: " + filepath.ToSlash(movies) + "\n" +
		"data_dir: data\n" +
		"subtitles:\n  preferred_languages: [Romanian, eng]\n" +
		"trickplay:\n  width: 480\n"
	cfg, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Path = filepath.Join(dir, "config.yaml")
	if err := cfg.Validate(ValidateOptions{SkipPortCheck: true, SkipFFmpeg: true}); err != nil {
		t.Fatal(err)
	}
	if cfg.Libraries[0].Path != movies || cfg.Libraries[1].Path != movies {
		t.Errorf("paths not normalised: %q %q", cfg.Libraries[0].Path, cfg.Libraries[1].Path)
	}
	if cfg.DataDir != filepath.Join(dir, "data") {
		t.Errorf("relative data_dir = %q", cfg.DataDir)
	}
	if got := strings.Join(cfg.Subtitles.PreferredLanguages, ","); got != "ro,en" {
		t.Errorf("preferred languages = %q", got)
	}
	// Unset keys inside a section keep their defaults.
	if cfg.Trickplay.Width != 480 || cfg.Trickplay.IntervalSeconds != 10 || !cfg.Trickplay.Enabled {
		t.Errorf("trickplay = %+v", cfg.Trickplay)
	}
}

func TestValidateReportsAllErrors(t *testing.T) {
	src := `port: 70000
libraries:
  - name: A
    type: films
    path: 'Z:\does\not\exist'
  - name: a
    type: shows
    path: relative/path
subtitles:
  preferred_languages: [klingon]
  fallback_encoding: nope
playback:
  audio_convert: sometimes
log_level: loud
`
	cfg, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Path = filepath.Join(t.TempDir(), "config.yaml")
	err = cfg.Validate(ValidateOptions{SkipPortCheck: true, SkipFFmpeg: true})
	var ve *Errors
	if !errors.As(err, &ve) {
		t.Fatalf("want *Errors, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"port: 70000", `type must be movies or shows, got "films"`, "is not accessible",
		"duplicate library name", "must be absolute", `unknown language "klingon"`, "fallback_encoding",
		"audio_convert", "log_level"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in:\n%s", want, msg)
		}
	}
	if len(ve.List) < 9 {
		t.Errorf("expected ≥ 9 errors, got %d:\n%s", len(ve.List), msg)
	}
}

func TestMetadataLanguage(t *testing.T) {
	for in, want := range map[string]string{"": "en-US", "ro-RO": "ro-RO", "ro_ro": "ro-RO", "RO": "ro", " en-us ": "en-US"} {
		if got, ok := normalizeMetadataLanguage(in); !ok || got != want {
			t.Errorf("%q → %q %v, want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"romanian", "ro-ROU", "r1", "ro-"} {
		if _, ok := normalizeMetadataLanguage(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	dir := t.TempDir()
	cfg := Defaults()
	cfg.Path = filepath.Join(dir, "config.yaml")
	cfg.Libraries = []Library{{Name: "M", Type: "movies", Path: dir}}
	cfg.MetadataLanguage = "Romanian"
	cfg.TMDBAPIKey = "  key  "
	err := cfg.Validate(ValidateOptions{SkipPortCheck: true, SkipFFmpeg: true})
	if err == nil || !strings.Contains(err.Error(), "metadata_language") {
		t.Errorf("want metadata_language error, got %v", err)
	}
	if cfg.TMDBAPIKey != "key" {
		t.Errorf("key not trimmed: %q", cfg.TMDBAPIKey)
	}
}

// The port check reports a port another program holds (Jellyfin on
// 0.0.0.0:8096, or wslrelay on 127.0.0.1 only). Tests stay on loopback:
// listening on 0.0.0.0 makes Windows Firewall prompt for every new test binary.
func TestPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	dir := t.TempDir()
	cfg := Defaults()
	cfg.Path = filepath.Join(dir, "config.yaml")
	cfg.Libraries = []Library{{Name: "M", Type: "movies", Path: dir}}
	cfg.Port = ln.Addr().(*net.TCPAddr).Port
	err = cfg.Validate(ValidateOptions{SkipFFmpeg: true, portCheckAddrs: []string{"127.0.0.1"}})
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Errorf("want port-in-use error, got %v", err)
	}
	ln.Close()
	if err := cfg.Validate(ValidateOptions{SkipFFmpeg: true, portCheckAddrs: []string{"127.0.0.1"}}); err != nil {
		t.Errorf("free port: %v", err)
	}
}

func TestFFmpegResolution(t *testing.T) {
	dir := t.TempDir()
	cfg := Defaults()
	cfg.Path = filepath.Join(dir, "config.yaml")
	cfg.Libraries = []Library{{Name: "M", Type: "movies", Path: dir}}
	cfg.FFmpeg = filepath.Join(dir, "missing-ffmpeg.exe")
	err := cfg.Validate(ValidateOptions{SkipPortCheck: true})
	if err == nil || !strings.Contains(err.Error(), "ffmpeg: configured path") {
		t.Errorf("want configured ffmpeg error, got %v", err)
	}
	if _, lookErr := exec.LookPath("ffprobe"); lookErr == nil && strings.Contains(err.Error(), "ffprobe:") {
		t.Errorf("ffprobe is on PATH but was reported missing: %v", err)
	}
}
