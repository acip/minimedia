package textutil

import "testing"

func TestFold(t *testing.T) {
	cases := map[string]string{
		"Știința Ușoară": "stiinta usoara",
		"ŞţĂÂÎ":          "staai",
		"Straße":         "strasse",
		"Łódź":           "lodz",
		"plain":          "plain",
	}
	for in, want := range cases {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchPrefixWords(t *testing.T) {
	cases := []struct {
		q, text string
		want    bool
	}{
		{"stiin", "Știința Ușoară", true},
		{"știința", "Stiinta Usoara", true},
		{"usoa sti", "Știința Ușoară", true},
		{"tiin", "Știința Ușoară", false}, // prefix only, not substring
		{"big movie", "Big Test Movie", true},
		{"big film", "Big Test Movie", false},
		{"", "anything", false},
		{"  ", "anything", false},
	}
	for _, c := range cases {
		if got := MatchPrefixWords(c.q, c.text); got != c.want {
			t.Errorf("MatchPrefixWords(%q, %q) = %v, want %v", c.q, c.text, got, c.want)
		}
	}
}

func TestSortKey(t *testing.T) {
	cases := map[string]string{
		"The General":        "general",
		"A Trip to the Moon": "trip to the moon",
		"Ana":                "ana",
		"The":                "the",
		"Ștefan":             "stefan",
	}
	for in, want := range cases {
		if got := SortKey(in); got != want {
			t.Errorf("SortKey(%q) = %q, want %q", in, got, want)
		}
	}
}
