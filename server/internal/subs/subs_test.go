package subs

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
	xunicode "golang.org/x/text/encoding/unicode"
)

func TestNormalizeLang(t *testing.T) {
	cases := map[string]string{
		"ro": "ro", "ron": "ro", "rum": "ro", "Romanian": "ro", "romana": "ro", "Română": "ro", "RO": "ro",
		"en": "en", "eng": "en", "English": "en", "ENGLISH": "en",
		"fre": "fr", "fra": "fr", "ger": "de", "deu": "de", "hun": "hu", "Magyar": "hu",
		"pt-BR": "pt", "en_US": "en", "zh-Hans": "zh", "chi": "zh", "nob": "no",
		"": "und", "xx": "und", "und": "und", "Klingon": "und", "commentary": "und",
	}
	for in, want := range cases {
		if got := NormalizeLang(in); got != want {
			t.Errorf("NormalizeLang(%q) = %q, want %q", in, got, want)
		}
	}
	if LangName("ro") != "Romanian" || LangName("und") != "Unknown" || LangName("") != "Unknown" {
		t.Error("LangName mismatch")
	}
}

func TestMatchName(t *testing.T) {
	stem := "Big Test Movie (2021)"
	cases := []struct {
		name  string
		ok    bool
		lang  string
		forc  bool
		sdh   bool
		label string
		codec string
	}{
		{"Big Test Movie (2021).srt", true, "und", false, false, "", "srt"},
		{"Big Test Movie (2021).ro.srt", true, "ro", false, false, "", "srt"},
		{"big test movie (2021).RO.SRT", true, "ro", false, false, "", "srt"},
		{"Big Test Movie (2021).en.forced.srt", true, "en", true, false, "", "srt"},
		{"Big Test Movie (2021).en.sdh.srt", true, "en", false, true, "", "srt"},
		{"Big Test Movie (2021).en.cc.srt", true, "en", false, true, "", "srt"},
		{"Big Test Movie (2021).Commentary.en.srt", true, "en", false, false, "Commentary", "srt"},
		{"Big Test Movie (2021).rum.ass", true, "ro", false, false, "", "ass"},
		{"Big Test Movie (2021).Romanian.ssa", true, "ro", false, false, "", "ssa"},
		{"Big Test Movie (2021).eng.vtt", true, "en", false, false, "", "vtt"},
		{"Big Test Movie (2021).forced.srt", true, "und", true, false, "", "srt"},
		{"Big Test Movie (2021).nfo", false, "", false, false, "", ""},
		{"Big Test Movie (2021) extra.srt", false, "", false, false, "", ""},
		{"Big Test.srt", false, "", false, false, "", ""},
		{"Other Movie.ro.srt", false, "", false, false, "", ""},
	}
	for _, c := range cases {
		info, ok := MatchName(stem, c.name)
		if ok != c.ok {
			t.Errorf("MatchName(%q) ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if ok && (info.Language != c.lang || info.Forced != c.forc || info.SDH != c.sdh || info.Label != c.label || info.Codec != c.codec) {
			t.Errorf("MatchName(%q) = %+v", c.name, info)
		}
	}
}

func TestParseLoose(t *testing.T) {
	cases := []struct {
		name string
		lang string
		forc bool
		sdh  bool
	}{
		{"2_English.srt", "en", false, false},
		{"3_Romanian.srt", "ro", false, false},
		{"4_English_Forced.srt", "en", true, false},
		{"5_English_SDH.srt", "en", false, true},
		{"6_French (France).srt", "fr", false, false},
		{"English.srt", "en", false, false},
		{"track7.srt", "und", false, false},
	}
	for _, c := range cases {
		info, ok := ParseLoose(c.name)
		if !ok || info.Language != c.lang || info.Forced != c.forc || info.SDH != c.sdh {
			t.Errorf("ParseLoose(%q) = %+v, %v", c.name, info, ok)
		}
	}
	if _, ok := ParseLoose("readme.txt"); ok {
		t.Error("ParseLoose accepted a .txt")
	}
}

func TestLabels(t *testing.T) {
	got := Labels([]Track{
		{Language: "ro"},
		{Language: "en", Forced: true},
		{Language: "en", SDH: true},
		{Language: "en"},
		{Language: "en"},
		{Language: "und"},
		{Language: "en", Extra: "Commentary"},
		{Language: "en", Extra: "English"},
		{Language: "es", SDH: true, Extra: "SDH"},
		{Language: "fr", Forced: true, Extra: "forced"},
	})
	want := []string{"Romanian", "English (Forced)", "English (SDH)", "English", "English #2", "Unknown", "English (Commentary)", "English #3",
		"Spanish (SDH)", "French (Forced)"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("label %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Romanian sample text. Legacy code pages only have the cedilla letters.
const roComma = "Bună ziua! Știința e ușoară. Țara mea, în dimineață, își amintește."
const roCedilla = "Bună ziua! Ştiinţa e uşoară. Ţara mea, în dimineaţă, îşi aminteşte."

func mustEncode(t *testing.T, cm *charmap.Charmap, s string) []byte {
	t.Helper()
	b, err := cm.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}

func TestToUTF8Windows1250(t *testing.T) {
	src := "1\r\n00:00:01,000 --> 00:00:04,000\r\n„" + roCedilla + "”\r\n"
	data := mustEncode(t, charmap.Windows1250, src)
	if data[0] != '1' || strings.Contains(string(data), "ă") {
		t.Fatal("sample is not legacy-encoded")
	}
	// The configured fallback is ISO-8859-2 here on purpose: the Romanian
	// heuristic must still pick Windows-1250 because „” are C1 controls in 8859-2.
	got, err := ToUTF8(data, "ro", charmap.ISO8859_2)
	if err != nil {
		t.Fatal(err)
	}
	want := "1\n00:00:01,000 --> 00:00:04,000\n„" + roComma + "”\n"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestToUTF8ISO88592(t *testing.T) {
	src := "1\n00:00:01,000 --> 00:00:04,000\n" + roCedilla + "\n"
	data := mustEncode(t, charmap.ISO8859_2, src)
	got, err := ToUTF8(data, "ro", charmap.Windows1250)
	if err != nil {
		t.Fatal(err)
	}
	if want := "1\n00:00:01,000 --> 00:00:04,000\n" + roComma + "\n"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestToUTF8RomanianWithCzechLetter(t *testing.T) {
	// "Š" is 0x8A in Windows-1250 but a C1 control in ISO-8859-2, so the
	// heuristic must choose Windows-1250 even when 8859-2 is the fallback.
	data := mustEncode(t, charmap.Windows1250, "Šerban şi Ţuţu")
	got, err := ToUTF8(data, "ro", charmap.ISO8859_2)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Šerban și Țuțu" {
		t.Errorf("got %q", got)
	}
}

func TestToUTF8NonRomanianUsesFallback(t *testing.T) {
	data := mustEncode(t, charmap.Windows1250, "Příliš žluťoučký kůň")
	got, err := ToUTF8(data, "cs", charmap.Windows1250)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Příliš žluťoučký kůň" {
		t.Errorf("got %q", got)
	}
	// Cedilla normalisation is Romanian-only.
	data = mustEncode(t, charmap.Windows1250, "Kuşadasi")
	if got, _ = ToUTF8(data, "tr", charmap.Windows1250); got != "Kuşadasi" {
		t.Errorf("got %q", got)
	}
}

func TestToUTF8BOMs(t *testing.T) {
	text := "1\r\n00:00:01,000 --> 00:00:02,000\r\nȘtiință\r\n"
	want := "1\n00:00:01,000 --> 00:00:02,000\nȘtiință\n"

	bom8 := append([]byte{0xEF, 0xBB, 0xBF}, []byte(text)...)
	if got, err := ToUTF8(bom8, "ro", charmap.Windows1250); err != nil || got != want {
		t.Errorf("UTF-8 BOM: %q, %v", got, err)
	}
	for _, e := range []xunicode.Endianness{xunicode.LittleEndian, xunicode.BigEndian} {
		b, err := xunicode.UTF16(e, xunicode.UseBOM).NewEncoder().Bytes([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := ToUTF8(b, "en", charmap.Windows1250); err != nil || got != want {
			t.Errorf("UTF-16 (%v): %q, %v", e, got, err)
		}
	}
	// Plain UTF-8 without BOM is kept, but Romanian cedillas still get fixed.
	if got, _ := ToUTF8([]byte("Ştiinţă\r"), "ro", charmap.Windows1250); got != "Știință\n" {
		t.Errorf("UTF-8 ro: %q", got)
	}
	if got, _ := ToUTF8([]byte("Ştiinţă"), "en", charmap.Windows1250); got != "Ştiinţă" {
		t.Errorf("UTF-8 en must not be touched: %q", got)
	}
}

func TestLookupEncoding(t *testing.T) {
	for _, n := range []string{"windows-1250", "Windows-1250", "cp1250", "iso-8859-2", "ISO-8859-1", "utf-8"} {
		if _, err := LookupEncoding(n); err != nil {
			t.Errorf("LookupEncoding(%q): %v", n, err)
		}
	}
	if _, err := LookupEncoding("nope-42"); err == nil {
		t.Error("expected error for unknown encoding")
	}
}
