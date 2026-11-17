package library

import (
	"io"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/acip/minimedia/server/internal/fsutil"
	"github.com/acip/minimedia/server/internal/store"
)

// Local artwork (PLAN.md 3.8) and tmdb.txt overrides (3.7) found next to media.

// imageExts in order of preference when the same base name exists twice.
var imageExts = []string{".jpg", ".jpeg", ".png", ".webp"}

func isImage(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range imageExts {
		if e == ext {
			return true
		}
	}
	return false
}

type imageFile struct {
	name string
	size int64
	mod  int64
}

// Base names (without extension) per kind, in priority order.
var (
	folderArt = map[string][]string{
		"poster":   {"poster", "folder", "cover"},
		"backdrop": {"fanart", "backdrop", "background"},
		"logo":     {"logo", "clearlogo"},
	}
	// Kodi-style "<video name>-poster.jpg" works for movies sharing a folder.
	stemArt = map[string][]string{
		"poster":   {"-poster"},
		"backdrop": {"-fanart", "-backdrop", "-background"},
		"logo":     {"-logo", "-clearlogo"},
		"still":    {"-thumb"},
	}
)

// findArt returns the first image in dir whose name (without extension)
// equals prefix+suffix for one of the candidates, in priority order.
func findArt(dir string, files []imageFile, prefix string, candidates []string) *store.Image {
	if len(files) == 0 {
		return nil
	}
	byBase := map[string][]imageFile{}
	for _, f := range files {
		base := strings.ToLower(strings.TrimSuffix(f.name, filepath.Ext(f.name)))
		byBase[base] = append(byBase[base], f)
	}
	for _, c := range candidates {
		matches := byBase[strings.ToLower(prefix+c)]
		for _, ext := range imageExts {
			for _, f := range matches {
				if strings.EqualFold(filepath.Ext(f.name), ext) {
					p := filepath.Join(dir, f.name)
					return &store.Image{Path: p, Source: "local",
						Version: hash16(NormPath(p) + "|" + strconv.FormatInt(f.size, 10) + "|" + strconv.FormatInt(f.mod, 10))[:8]}
				}
			}
		}
	}
	return nil
}

// movieArt finds a movie's artwork: "<name>-poster.jpg" style files first,
// then generic names (poster.jpg, fanart.jpg …) when the movie has its own folder.
func movieArt(dir, stem string, files []imageFile, ownFolder bool) []store.Image {
	var out []store.Image
	for _, kind := range []string{"poster", "backdrop", "logo"} {
		im := findArt(dir, files, stem, stemArt[kind])
		if im == nil && ownFolder {
			im = findArt(dir, files, "", folderArt[kind])
		}
		if im != nil {
			im.Kind = kind
			out = append(out, *im)
		}
	}
	return out
}

// showArt finds artwork in a show folder.
func showArt(dir string, files []imageFile) []store.Image {
	var out []store.Image
	for _, kind := range []string{"poster", "backdrop", "logo"} {
		if im := findArt(dir, files, "", folderArt[kind]); im != nil {
			im.Kind = kind
			out = append(out, *im)
		}
	}
	return out
}

// episodeArt finds "<episode name>-thumb.jpg".
func episodeArt(dir, stem string, files []imageFile) []store.Image {
	if im := findArt(dir, files, stem, stemArt["still"]); im != nil {
		im.Kind = "still"
		return []store.Image{*im}
	}
	return nil
}

// readHint reads a TMDB id from tmdb.txt. It accepts a bare id ("603"),
// "tmdb-603", or a themoviedb.org URL; the first run of digits wins.
func readHint(path string) int64 {
	f, err := fsutil.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, 4096))
	return parseHint(string(data))
}

func parseHint(s string) int64 {
	start := strings.IndexFunc(s, unicode.IsDigit)
	if start < 0 {
		return 0
	}
	end := start
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, err := strconv.ParseInt(s[start:end], 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func imageFileOf(name string, fi fs.FileInfo) imageFile {
	return imageFile{name: name, size: fi.Size(), mod: fi.ModTime().UnixMilli()}
}
