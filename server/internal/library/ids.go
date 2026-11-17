package library

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/acip/minimedia/server/internal/fsutil"
)

// NormPath normalises an absolute path for ids and de-duplication: cleaned,
// Unicode NFC, forward slashes, and lower-cased where the OS is case-insensitive.
func NormPath(p string) string {
	p = norm.NFC.String(filepath.Clean(p))
	if fsutil.CaseInsensitive {
		p = strings.ToLower(p)
	}
	return filepath.ToSlash(p)
}

func hash16(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// ItemID is the stable id of a media file: the first 16 hex chars of the
// SHA-1 of its normalised absolute path.
func ItemID(path string) string { return hash16(NormPath(path)) }

// ShowID is the stable id of a show folder.
func ShowID(folder string) string { return hash16("show:" + NormPath(folder)) }

// SubtitleID identifies a subtitle of an item: an external file or an embedded stream.
func SubtitleID(itemID, key string) string { return hash16("sub:" + itemID + "|" + key)[:12] }
