// Package textutil holds small text helpers shared by the scanner and the API.
package textutil

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Letters that don't decompose into base + combining mark under NFD.
var specialFold = strings.NewReplacer(
	"ß", "ss", "đ", "d", "Đ", "d", "ł", "l", "Ł", "l", "ø", "o", "Ø", "o",
	"æ", "ae", "Æ", "ae", "œ", "oe", "Œ", "oe", "ı", "i", "þ", "th", "Þ", "th",
)

// Fold lower-cases s and strips diacritics, so "Știința" and "stiinta" compare equal.
func Fold(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	return strings.ToLower(specialFold.Replace(out))
}

// Words splits folded text into alphanumeric words.
func Words(s string) []string {
	return strings.FieldsFunc(Fold(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// MatchPrefixWords reports whether every word of query is a prefix of some word
// in text (diacritics-insensitive). An empty query matches nothing.
func MatchPrefixWords(query, text string) bool {
	q := Words(query)
	if len(q) == 0 {
		return false
	}
	words := Words(text)
	for _, qw := range q {
		found := false
		for _, w := range words {
			if strings.HasPrefix(w, qw) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// SortKey returns a key for alphabetical sorting: folded, leading article removed.
func SortKey(title string) string {
	k := strings.TrimSpace(Fold(title))
	for _, a := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(k, a) && len(k) > len(a) {
			k = k[len(a):]
			break
		}
	}
	return k
}
