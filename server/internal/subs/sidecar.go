package subs

import (
	"fmt"
	"path/filepath"
	"strings"
)

var subExts = map[string]string{".srt": "srt", ".ass": "ass", ".ssa": "ssa", ".vtt": "vtt"}

// Codec returns "srt", "ass", "ssa" or "vtt" for a subtitle file name, or "".
func Codec(name string) string { return subExts[strings.ToLower(filepath.Ext(name))] }

// IsSubtitle reports whether name has a supported sidecar extension.
func IsSubtitle(name string) bool { return Codec(name) != "" }

// MIME returns the content type the TV app expects for a subtitle codec.
func MIME(codec string) string {
	switch codec {
	case "srt", "subrip":
		return "application/x-subrip"
	case "ass", "ssa":
		return "text/x-ssa"
	case "vtt", "webvtt":
		return "text/vtt"
	}
	return "application/octet-stream"
}

// SubFolders are the subfolder names (case-insensitive) searched for sidecars.
var SubFolders = []string{"subs", "subtitles"}

// IsSubFolder reports whether a directory name is a subtitles folder.
func IsSubFolder(name string) bool {
	for _, s := range SubFolders {
		if strings.EqualFold(name, s) {
			return true
		}
	}
	return false
}

// Info describes a sidecar parsed from its file name.
type Info struct {
	Language string // ISO 639-1 or "und"
	Forced   bool
	SDH      bool
	Label    string // extra user label from the file name, e.g. "Commentary"
	Codec    string
}

// MatchName checks whether subName is a sidecar for a video whose file stem is
// videoStem: "X.srt", "X.<lang>.srt", "X.<lang>.forced.srt", "X.<lang>.sdh.srt",
// "X.<lang>.cc.srt", "X.<label>.<lang>.srt". Comparison is case-insensitive.
// When several videos share a prefix, the caller should prefer the longest stem.
func MatchName(videoStem, subName string) (Info, bool) {
	codec := Codec(subName)
	if codec == "" {
		return Info{}, false
	}
	stem := strings.TrimSuffix(subName, filepath.Ext(subName))
	if len(stem) < len(videoStem) || !strings.EqualFold(stem[:len(videoStem)], videoStem) {
		return Info{}, false
	}
	rest := stem[len(videoStem):]
	if rest != "" && rest[0] != '.' {
		return Info{}, false
	}
	info := parseTokens(strings.Split(strings.TrimPrefix(rest, "."), "."))
	info.Codec = codec
	return info, true
}

// ParseLoose parses a sidecar inside a per-video folder (Subs/X/2_English.srt),
// where the whole name is free-form and only language/flags are extracted.
func ParseLoose(subName string) (Info, bool) {
	codec := Codec(subName)
	if codec == "" {
		return Info{}, false
	}
	stem := strings.TrimSuffix(subName, filepath.Ext(subName))
	toks := strings.FieldsFunc(stem, func(r rune) bool {
		return r == '.' || r == '_' || r == '-' || r == ' ' || r == '(' || r == ')' || r == '[' || r == ']'
	})
	info := Info{Language: "und", Codec: codec}
	for _, t := range toks {
		lt := strings.ToLower(t)
		switch {
		case lt == "forced":
			info.Forced = true
		case lt == "sdh" || lt == "cc":
			info.SDH = true
		default:
			// Leading track numbers ("2_English") and short junk are skipped;
			// two-letter codes only count when they are the whole token.
			if c, ok := LookupLang(t); ok && info.Language == "und" {
				info.Language = c
			}
		}
	}
	return info, true
}

func parseTokens(toks []string) Info {
	info := Info{Language: "und"}
	var labels []string
	// Walk from the end: flags and the language come last in common layouts.
	for i := len(toks) - 1; i >= 0; i-- {
		t := strings.TrimSpace(toks[i])
		lt := strings.ToLower(t)
		switch {
		case t == "":
		case lt == "forced" || lt == "foreign":
			info.Forced = true
		case lt == "sdh" || lt == "cc":
			info.SDH = true
		case lt == "default":
		default:
			if c, ok := LookupLang(t); ok && info.Language == "und" {
				info.Language = c
				continue
			}
			labels = append([]string{t}, labels...)
		}
	}
	info.Label = strings.Join(labels, " ")
	return info
}

// Track is anything that can be labelled for the user.
type Track struct {
	Language string
	Forced   bool
	SDH      bool
	Extra    string // optional extra label ("Commentary", embedded track title)
}

// Labels builds display labels ("Romanian", "English (Forced)", "English (SDH)")
// for tracks in order; duplicates get " #2", " #3"...
func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func Labels(tracks []Track) []string {
	out := make([]string, len(tracks))
	seen := map[string]int{}
	for i, t := range tracks {
		var extras []string
		if t.Forced {
			extras = append(extras, "Forced")
		}
		if t.SDH {
			extras = append(extras, "SDH")
		}
		// A track title that only repeats the language or a flag ("SDH",
		// "Forced", "English") adds nothing.
		if x := strings.TrimSpace(t.Extra); x != "" && !strings.EqualFold(x, LangName(t.Language)) && !containsFold(extras, x) {
			extras = append(extras, x)
		}
		l := LangName(t.Language)
		if len(extras) > 0 {
			l += " (" + strings.Join(extras, ", ") + ")"
		}
		seen[l]++
		if n := seen[l]; n > 1 {
			l = fmt.Sprintf("%s #%d", l, n)
		}
		out[i] = l
	}
	return out
}
