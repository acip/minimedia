// Package subs handles subtitle languages, sidecar file matching and
// conversion of subtitle text to UTF-8 (PLAN.md section 3.4).
package subs

import (
	"strings"

	"github.com/acip/minimedia/server/internal/textutil"
)

type langInfo struct {
	code  string   // ISO 639-1
	name  string   // English display name
	alias []string // 639-2/T, 639-2/B, English and native names (folded, lower case)
}

var languages = []langInfo{
	{"ro", "Romanian", []string{"ron", "rum", "romanian", "romana", "rom", "moldavian", "mol"}},
	{"en", "English", []string{"eng", "english"}},
	{"fr", "French", []string{"fra", "fre", "french", "francais"}},
	{"de", "German", []string{"deu", "ger", "german", "deutsch"}},
	{"es", "Spanish", []string{"spa", "spanish", "espanol", "castellano"}},
	{"it", "Italian", []string{"ita", "italian", "italiano"}},
	{"pt", "Portuguese", []string{"por", "portuguese", "portugues", "brazilian"}},
	{"hu", "Hungarian", []string{"hun", "hungarian", "magyar"}},
	{"bg", "Bulgarian", []string{"bul", "bulgarian"}},
	{"ru", "Russian", []string{"rus", "russian"}},
	{"uk", "Ukrainian", []string{"ukr", "ukrainian"}},
	{"pl", "Polish", []string{"pol", "polish", "polski"}},
	{"cs", "Czech", []string{"ces", "cze", "czech"}},
	{"sk", "Slovak", []string{"slk", "slo", "slovak"}},
	{"sl", "Slovenian", []string{"slv", "slovenian", "slovene"}},
	{"hr", "Croatian", []string{"hrv", "croatian"}},
	{"sr", "Serbian", []string{"srp", "serbian"}},
	{"bs", "Bosnian", []string{"bos", "bosnian"}},
	{"mk", "Macedonian", []string{"mkd", "mac", "macedonian"}},
	{"sq", "Albanian", []string{"sqi", "alb", "albanian"}},
	{"el", "Greek", []string{"ell", "gre", "greek"}},
	{"tr", "Turkish", []string{"tur", "turkish"}},
	{"nl", "Dutch", []string{"nld", "dut", "dutch"}},
	{"sv", "Swedish", []string{"swe", "swedish"}},
	{"no", "Norwegian", []string{"nor", "nob", "nno", "norwegian", "nb", "nn"}},
	{"da", "Danish", []string{"dan", "danish"}},
	{"fi", "Finnish", []string{"fin", "finnish"}},
	{"et", "Estonian", []string{"est", "estonian"}},
	{"lv", "Latvian", []string{"lav", "latvian"}},
	{"lt", "Lithuanian", []string{"lit", "lithuanian"}},
	{"he", "Hebrew", []string{"heb", "hebrew", "iw"}},
	{"ar", "Arabic", []string{"ara", "arabic"}},
	{"fa", "Persian", []string{"fas", "per", "persian", "farsi"}},
	{"hi", "Hindi", []string{"hin", "hindi"}},
	{"zh", "Chinese", []string{"zho", "chi", "chinese", "mandarin", "cantonese"}},
	{"ja", "Japanese", []string{"jpn", "japanese"}},
	{"ko", "Korean", []string{"kor", "korean"}},
	{"th", "Thai", []string{"tha", "thai"}},
	{"vi", "Vietnamese", []string{"vie", "vietnamese"}},
	{"id", "Indonesian", []string{"ind", "indonesian"}},
	{"ms", "Malay", []string{"msa", "may", "malay"}},
	{"ca", "Catalan", []string{"cat", "catalan"}},
	{"eu", "Basque", []string{"eus", "baq", "basque"}},
	{"gl", "Galician", []string{"glg", "galician"}},
	{"is", "Icelandic", []string{"isl", "ice", "icelandic"}},
	{"ga", "Irish", []string{"gle", "irish"}},
	{"cy", "Welsh", []string{"cym", "wel", "welsh"}},
	{"la", "Latin", []string{"lat", "latin"}},
}

var langIndex = func() map[string]*langInfo {
	m := map[string]*langInfo{}
	for i := range languages {
		l := &languages[i]
		m[l.code] = l
		for _, a := range l.alias {
			m[a] = l
		}
	}
	return m
}()

// NormalizeLang maps an ISO 639-1/639-2 code, a "pt-BR"-style tag or an
// English/native language name to ISO 639-1. Unknown input returns "und".
func NormalizeLang(s string) string {
	if c, ok := LookupLang(s); ok {
		return c
	}
	return "und"
}

// LookupLang is NormalizeLang that reports whether s was recognised.
func LookupLang(s string) (string, bool) {
	k := textutil.Fold(strings.TrimSpace(s))
	if k == "" {
		return "", false
	}
	if l, ok := langIndex[k]; ok {
		return l.code, true
	}
	// "pt-BR", "en_US", "zh-Hans"
	if i := strings.IndexAny(k, "-_"); i > 0 {
		if l, ok := langIndex[k[:i]]; ok && len(k[:i]) <= 3 {
			return l.code, true
		}
	}
	return "", false
}

// LangName returns the English name for an ISO 639-1 code ("ro" -> "Romanian").
func LangName(code string) string {
	if l, ok := langIndex[code]; ok {
		return l.name
	}
	if code == "" || code == "und" {
		return "Unknown"
	}
	return strings.ToUpper(code)
}
