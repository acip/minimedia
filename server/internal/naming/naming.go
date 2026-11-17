// Package naming parses movie and episode information out of file and folder
// names (PLAN.md section 3.2). It is pure string handling with no file I/O.
package naming

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".mov": true, ".avi": true,
	".ts": true, ".m2ts": true, ".webm": true, ".wmv": true,
}

// IsVideo reports whether name has a supported video extension.
func IsVideo(name string) bool {
	return videoExts[strings.ToLower(filepath.Ext(name))]
}

var ignoredDirs = map[string]bool{
	"extras": true, "featurettes": true, "behind the scenes": true, "deleted scenes": true,
	"interviews": true, "trailers": true, "samples": true, "sample": true, "other": true,
	"$recycle.bin": true, "system volume information": true, "@eadir": true, ".trickplay": true,
}

// IgnoredDir reports whether a folder with this name is skipped during scans.
// Dot-folders are treated as hidden on every OS.
func IgnoredDir(name string) bool {
	return ignoredDirs[strings.ToLower(name)] || strings.HasPrefix(name, ".")
}

// SampleMaxBytes is the size under which a file with a "sample" token is ignored.
const SampleMaxBytes = 300 << 20

// IsSample reports whether a file is a release sample: "sample" is the first
// or last word of the name ("movie-sample.mkv", "Movie.2020.SAMPLE.mkv",
// "sample.mkv") and the file is under 300 MB. A word in the middle of a title
// ("HDR Sample (2020).mkv") doesn't count.
func IsSample(name string, size int64) bool {
	if size >= SampleMaxBytes {
		return false
	}
	toks := strings.FieldsFunc(strings.TrimSuffix(name, filepath.Ext(name)), notAlnum)
	if len(toks) == 0 {
		return false
	}
	return strings.EqualFold(toks[0], "sample") || strings.EqualFold(toks[len(toks)-1], "sample")
}

func notAlnum(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

// Movie is the result of parsing a movie file or folder name.
type Movie struct {
	Title string
	Year  int // 0 when unknown
}

var folderYearRe = regexp.MustCompile(`^(.+?)[\s._]*\((\d{4})\)$`)

// ParseFolder parses "Title (Year)" folder names. Bracketed groups such as
// "[tmdbid-123]" or "[1080p]" are ignored. ok is false for anything else.
func ParseFolder(name string) (m Movie, ok bool) {
	name = strings.Join(strings.Fields(bracketRe.ReplaceAllString(name, " ")), " ")
	sm := folderYearRe.FindStringSubmatch(name)
	if sm == nil {
		return Movie{}, false
	}
	y, _ := strconv.Atoi(sm[2])
	if !validYear(y) {
		return Movie{}, false
	}
	title := cleanSpaces(sm[1])
	if title == "" {
		return Movie{}, false
	}
	return Movie{Title: title, Year: y}, true
}

// ParseMovie parses a movie file name (with or without extension), including
// scene names like "Title.Year.1080p.BluRay.x264-GRP.mkv".
func ParseMovie(name string) Movie {
	stem := stripExt(name)
	if m, ok := ParseFolder(stem); ok {
		return m
	}
	title, year := cleanName(stem, true)
	if title == "" {
		title = strings.TrimSpace(stem)
	}
	return Movie{Title: title, Year: year}
}

// ParseShowFolder parses a show folder name: "Show Name (2020)" or a scene-ish name.
func ParseShowFolder(name string) Movie {
	if m, ok := ParseFolder(name); ok {
		return m
	}
	title, year := cleanName(name, true)
	if title == "" {
		title = strings.TrimSpace(name)
	}
	return Movie{Title: title, Year: year}
}

var (
	seasonFolderRe = regexp.MustCompile(`(?i)^(?:season|series|staffel|saison|sezonul|sezon)[\s._-]*(\d{1,4})\b`)
	shortSeasonRe  = regexp.MustCompile(`(?i)^s(\d{1,3})$`)
	specialsRe     = regexp.MustCompile(`(?i)^(?:specials?|season[\s._-]*0+)$`)
)

// ParseSeasonFolder returns the season number for "Season 01", "Season 1",
// "S01" and "Specials" (season 0).
func ParseSeasonFolder(name string) (int, bool) {
	name = strings.TrimSpace(name)
	if specialsRe.MatchString(name) {
		return 0, true
	}
	for _, re := range []*regexp.Regexp{seasonFolderRe, shortSeasonRe} {
		if sm := re.FindStringSubmatch(name); sm != nil {
			n, _ := strconv.Atoi(sm[1])
			return n, true
		}
	}
	return 0, false
}

// Episode is the result of parsing an episode file name.
type Episode struct {
	Season     int
	HasSeason  bool // false when only "E02"/"Ep 02" was found; take the season from the folder
	Episode    int
	EpisodeEnd int // last episode of a multi-episode file, 0 if single
	Title      string
}

var (
	// S01E02, s01e02, S01E02-E03, S01E02E03, S01E02-03, S01.E02
	sxeRe = regexp.MustCompile(`(?i)\bS(\d{1,3})[\s._-]?E(\d{1,4})((?:[\s._-]?-?[\s._-]?E\d{1,4})+|-\d{1,4}\b)?`)
	// 1x02, 1x02-1x03, 1x02x03
	nxnRe = regexp.MustCompile(`(?i)(?:^|[^\dA-Za-z])(\d{1,2})x(\d{2,3})((?:-\d{1,2}x\d{2,3})|(?:x\d{2,3})+)?(?:$|[^\dA-Za-z])`)
	// E02, Ep 02, Ep.02, Episode 2
	epOnlyRe  = regexp.MustCompile(`(?i)(?:^|[^A-Za-z])(?:E|Ep|Episode)[\s._-]?(\d{1,4})(?:$|[^\dA-Za-z])`)
	lastNumRe = regexp.MustCompile(`(\d{1,4})\D*$`)
)

// ParseEpisode extracts season/episode numbers and the episode title from a
// file name. ok is false when no episode number is found.
func ParseEpisode(name string) (e Episode, ok bool) {
	stem := stripExt(name)
	var rest string

	if loc := sxeRe.FindStringSubmatchIndex(stem); loc != nil {
		e.Season = atoi(stem[loc[2]:loc[3]])
		e.Episode = atoi(stem[loc[4]:loc[5]])
		e.HasSeason = true
		if loc[6] >= 0 {
			if sm := lastNumRe.FindStringSubmatch(stem[loc[6]:loc[7]]); sm != nil {
				e.EpisodeEnd = atoi(sm[1])
			}
		}
		rest = stem[loc[1]:]
	} else if loc := nxnRe.FindStringSubmatchIndex(stem); loc != nil {
		e.Season = atoi(stem[loc[2]:loc[3]])
		e.Episode = atoi(stem[loc[4]:loc[5]])
		e.HasSeason = true
		if loc[6] >= 0 {
			if sm := lastNumRe.FindStringSubmatch(stem[loc[6]:loc[7]]); sm != nil {
				e.EpisodeEnd = atoi(sm[1])
			}
		}
		rest = stem[loc[5]:]
		if loc[6] >= 0 {
			rest = stem[loc[7]:]
		}
	} else if loc := epOnlyRe.FindStringSubmatchIndex(stem); loc != nil {
		e.Episode = atoi(stem[loc[2]:loc[3]])
		rest = stem[loc[3]:]
	} else {
		return Episode{}, false
	}
	if e.EpisodeEnd <= e.Episode {
		e.EpisodeEnd = 0
	}
	e.Title, _ = cleanName(rest, false)
	return e, true
}

// LooseEpisode is an episode found outside a shows library (a movies library
// that mixes in TV, PLAN.md 3.2): the show name comes from the text before the
// episode code.
type LooseEpisode struct {
	Episode
	Show Movie // Title is empty when the name starts with the code ("S01E02.mkv")
}

// ParseLooseEpisode recognises episodes by an explicit season and episode
// code (S01E02 or 1x02) only; "Episode 4" or "E02" alone are too common in
// movie titles. ok is false for anything else.
func ParseLooseEpisode(name string) (le LooseEpisode, ok bool) {
	stem := stripExt(name)
	start := -1
	if loc := sxeRe.FindStringIndex(stem); loc != nil {
		start = loc[0]
	} else if loc := nxnRe.FindStringSubmatchIndex(stem); loc != nil {
		start = loc[2]
	} else {
		return LooseEpisode{}, false
	}
	e, ok := ParseEpisode(name)
	if !ok || !e.HasSeason {
		return LooseEpisode{}, false
	}
	title, year := cleanName(stem[:start], true)
	return LooseEpisode{Episode: e, Show: Movie{Title: title, Year: year}}, true
}

// "Show.Name.S01.1080p.WEB-DL…" or "Show Name Season 2": a folder holding one
// season of a show.
var seasonPackRe = regexp.MustCompile(`(?i)(?:^|[\s._-])(?:S(\d{1,3})|(?:Season|Sezonul|Sezon)[\s._-]*(\d{1,3}))(?:$|[\s._-])`)

// ParseSeasonPack parses a season-pack folder name into the show and season.
// ok is false when the name has no season marker or nothing before it.
func ParseSeasonPack(name string) (show Movie, season int, ok bool) {
	loc := seasonPackRe.FindStringSubmatchIndex(name)
	if loc == nil {
		return Movie{}, 0, false
	}
	if loc[2] >= 0 {
		season = atoi(name[loc[2]:loc[3]])
	} else {
		season = atoi(name[loc[4]:loc[5]])
	}
	title, year := cleanName(name[:loc[0]], true)
	if title == "" {
		return Movie{}, 0, false
	}
	return Movie{Title: title, Year: year}, season, true
}

// Quality/source/codec tags. A scene name is cut at the first of these.
var tags = map[string]bool{
	"2160p": true, "1080p": true, "1080i": true, "720p": true, "576p": true, "480p": true, "4k": true,
	"bluray": true, "blu-ray": true, "bdrip": true, "brrip": true, "bdremux": true, "web-dl": true,
	"webdl": true, "webrip": true, "hdtv": true, "hdrip": true, "dvdrip": true, "dvdscr": true,
	"remux": true, "hdr10": true, "hdr10+": true, "dovi": true, "x264": true, "x265": true,
	"h264": true, "h265": true, "h.264": true, "h.265": true, "hevc": true, "avc": true, "av1": true,
	"xvid": true, "divx": true, "aac": true, "ac3": true, "eac3": true, "dts": true, "dts-hd": true,
	"truehd": true, "ddp5.1": true, "ddp5": true, "dd5.1": true, "dd5": true, "ddp": true,
	"atmos": true, "10bit": true, "8bit": true, "amzn": true, "nf": true, "dsnp": true, "hmax": true,
}

// Tags that are also ordinary words ("The Spider's Web", "The Proper Way"):
// they only cut when the next token is also a tag.
var weakTags = map[string]bool{
	"web": true, "hdr": true, "dv": true, "proper": true, "repack": true, "uhd": true,
	"sdr": true, "imax": true, "multi": true, "dual": true, "internal": true, "extended": true,
}

var bracketRe = regexp.MustCompile(`\[[^\]]*\]|\{[^}]*\}`)

// cleanName turns a scene-style name into a title. With findYear, a year token
// (1900–2099, not the first token) ends the title and is returned.
func cleanName(s string, findYear bool) (title string, year int) {
	s = bracketRe.ReplaceAllString(s, " ")
	// Protect dotted tags before turning dots into spaces.
	low := strings.ToLower(s)
	for _, t := range []string{"ddp5.1", "dd5.1", "h.264", "h.265", "ddp2.0", "dd2.0", "aac2.0", "aac5.1"} {
		for {
			i := strings.Index(low, t)
			if i < 0 {
				break
			}
			repl := strings.ReplaceAll(t, ".", "")
			s = s[:i] + repl + s[i+len(t):]
			low = low[:i] + repl + low[i+len(t):]
		}
	}
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	toks := strings.Fields(s)

	isTag := func(i int) bool {
		t := tagForm(toks[i])
		if tags[t] || tags[strings.ReplaceAll(t, ".", "")] {
			return true
		}
		return false
	}
	isTagOrWeak := func(i int) bool { return isTag(i) || weakTags[tagForm(toks[i])] }

	cut := len(toks)
	for i := range toks {
		if i == 0 && findYear {
			continue // a title can start with anything, even "1080p" or "2012"
		}
		if isTag(i) {
			cut = i
			break
		}
		if weakTags[tagForm(toks[i])] && i+1 < len(toks) && isTagOrWeak(i+1) {
			cut = i
			break
		}
	}

	if findYear {
		// The year is the last year-looking token before the cut (so
		// "Expedition 2050 2018" keeps 2050 in the title), preferring "(2018)".
		yi, paren := -1, false
		for i := 1; i < cut; i++ {
			y, p := yearToken(toks[i])
			if y == 0 {
				continue
			}
			if p || !paren {
				yi, paren = i, p
				year = y
			}
		}
		if yi > 0 {
			cut = yi
		}
	}
	title = cleanSpaces(strings.Join(toks[:cut], " "))
	return title, year
}

// tagForm lower-cases a token and strips surrounding punctuation and a
// release-group suffix ("x264-GRP" -> "x264").
func tagForm(tok string) string {
	t := strings.ToLower(strings.Trim(tok, "()-,;:!"))
	if tags[t] {
		return t
	}
	if i := strings.IndexByte(t, '-'); i > 0 {
		return t[:i]
	}
	return t
}

func yearToken(tok string) (year int, paren bool) {
	t := tok
	if strings.HasPrefix(t, "(") && strings.HasSuffix(t, ")") {
		t, paren = t[1:len(t)-1], true
	}
	if len(t) != 4 {
		return 0, false
	}
	y, err := strconv.Atoi(t)
	if err != nil || !validYear(y) {
		return 0, false
	}
	return y, paren
}

func validYear(y int) bool { return y >= 1900 && y <= 2099 }

// cleanSpaces collapses whitespace and trims separators left at the ends.
func cleanSpaces(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, " -–—._,:;")
	// A dangling "(" or "()" left by a cut year.
	s = strings.TrimSpace(strings.TrimSuffix(s, "()"))
	s = strings.TrimSpace(strings.TrimSuffix(s, "("))
	return strings.Trim(s, " -–—._,:;")
}

func stripExt(name string) string {
	if IsVideo(name) {
		return strings.TrimSuffix(name, filepath.Ext(name))
	}
	return name
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
