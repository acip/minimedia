package subs

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
	xunicode "golang.org/x/text/encoding/unicode"
)

// LookupEncoding resolves a config encoding name ("windows-1250", "iso-8859-2", ...).
func LookupEncoding(name string) (encoding.Encoding, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	// htmlindex follows WHATWG and maps these to windows-1252/1254; keep them exact.
	case "iso-8859-1", "latin1", "latin-1":
		return charmap.ISO8859_1, nil
	case "iso-8859-9", "latin5":
		return charmap.ISO8859_9, nil
	}
	e, err := htmlindex.Get(name)
	if err != nil {
		return nil, fmt.Errorf("unknown encoding %q", name)
	}
	return e, nil
}

// ToUTF8 converts subtitle bytes to UTF-8 text with "\n" line endings:
//  1. a UTF-8 BOM is stripped; UTF-16 LE/BE with BOM is decoded;
//  2. valid UTF-8 is kept;
//  3. otherwise the bytes are decoded with fallback; for Romanian ("ro")
//     Windows-1250 and ISO-8859-2 are both tried and the more plausible wins;
//  4. Romanian cedilla letters are normalised to comma-below (ro only);
//  5. line endings are normalised to "\n".
func ToUTF8(data []byte, lang string, fallback encoding.Encoding) (string, error) {
	var text string
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		text = string(data[3:])
		if !utf8.ValidString(text) {
			text = strings.ToValidUTF8(text, "�")
		}
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}), bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		dec := xunicode.UTF16(xunicode.LittleEndian, xunicode.ExpectBOM).NewDecoder()
		b, err := dec.Bytes(data)
		if err != nil {
			return "", fmt.Errorf("decode UTF-16: %w", err)
		}
		text = string(b)
	case utf8.Valid(data):
		text = string(data)
	default:
		candidates := []encoding.Encoding{fallback}
		if lang == "ro" {
			candidates = []encoding.Encoding{fallback, charmap.Windows1250, charmap.ISO8859_2}
		}
		best, bestScore := "", -1<<31
		for _, enc := range candidates {
			if enc == nil {
				continue
			}
			b, err := enc.NewDecoder().Bytes(data)
			if err != nil {
				continue
			}
			s := string(b)
			if sc := score(s, lang); sc > bestScore { // ties keep the earlier (configured) one
				best, bestScore = s, sc
			}
		}
		if bestScore == -1<<31 {
			return "", fmt.Errorf("could not decode subtitle with fallback encoding")
		}
		text = best
	}
	if lang == "ro" {
		text = FixRomanian(text)
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text, nil
}

// score rates how plausible decoded text is: Romanian letters count up,
// control characters and letters foreign to the language count down.
func score(s, lang string) int {
	sc := 0
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
		case r == utf8.RuneError || unicode.IsControl(r): // undefined bytes, C0/C1 controls
			sc -= 10
		case lang == "ro" && strings.ContainsRune("ăâîșțşţĂÂÎȘȚŞŢ", r):
			sc += 2
		case lang == "ro" && strings.ContainsRune("ĄąĽľŚśŠšŤťŹźŽžŁłŃńŘřŮůŐőŰűĹĺ", r):
			sc -= 3 // Polish/Czech/Slovak/Hungarian letters: likely the wrong code page
		}
	}
	return sc
}

var roFix = strings.NewReplacer("ş", "ș", "Ş", "Ș", "ţ", "ț", "Ţ", "Ț")

// FixRomanian converts cedilla s/t to the correct comma-below letters.
func FixRomanian(s string) string { return roFix.Replace(s) }
