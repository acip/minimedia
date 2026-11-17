package naming

import "testing"

func TestParseMovie(t *testing.T) {
	cases := []struct {
		in    string
		title string
		year  int
	}{
		// Plain "Title (Year)" forms
		{"Big Test Movie (2021).mp4", "Big Test Movie", 2021},
		{"HDR Sample (2020).mkv", "HDR Sample", 2020},
		{"The General (1926).mkv", "The General", 1926},
		{"Les Misérables (1925).mkv", "Les Misérables", 1925},
		{"Știința Ușoară (2018).mp4", "Știința Ușoară", 2018},
		{"Independența României (1912).avi", "Independența României", 1912},
		{"Big Buck Bunny (2008) [tmdbid-10378].mkv", "Big Buck Bunny", 2008},
		{"1999 Home Movies (2004).mkv", "1999 Home Movies", 2004},
		{"Expedition 2050 (2018).mkv", "Expedition 2050", 2018},
		{"1999 (2019).mp4", "1999", 2019},
		{"Movie Without Year.mkv", "Movie Without Year", 0},
		// Scene names
		{"Irregular.Keys.2019.1080p.BluRay.x264-GRP.mkv", "Irregular Keys", 2019},
		{"Tears.of.Steel.2012.2160p.UHD.BluRay.REMUX.HDR.HEVC.Atmos-GRP.mkv", "Tears of Steel", 2012},
		{"Expedition.2050.2018.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv", "Expedition 2050", 2018},
		{"1999.2019.720p.BRRip.x264.AAC.mp4", "1999", 2019},
		{"Sintel_2010_1080p_BluRay_x265.mkv", "Sintel", 2010},
		{"Some.Movie.1080p.WEBRip.x264.mkv", "Some Movie", 0},
		{"[Blender] Cosmos Laundromat (2015) [1080p] [BluRay].mp4", "Cosmos Laundromat", 2015},
		{"[Group] Elephants.Dream.2006.BDRip.mkv", "Elephants Dream", 2006},
		{"Sprite.Fright.2021.4K.HDR.DV.2160p.mkv", "Sprite Fright", 2021},
		{"The.Spider's.Web.1913.DVDRip.XviD.avi", "The Spider's Web", 1913},
		{"The Spider's Web.mkv", "The Spider's Web", 0},
		{"Film.WEB.H264-GRP.mkv", "Film", 0},
		{"The.Jack-Knife.Man.1920.1080p.mkv", "The Jack-Knife Man", 1920},
		{"X-Ray.2000.hevc.mkv", "X-Ray", 2000},
		{"Știința.Ușoară.2018.1080p.WEB-DL.mkv", "Știința Ușoară", 2018},
		{"Independenta.Romaniei.1912.RoSubbed.DVDRip.mkv", "Independenta Romaniei", 1912},
		{"2050.2009.1080p.BluRay.mkv", "2050", 2009},
		{"movie.title.2021.HDTV.h265.mkv", "movie title", 2021},
		{"Night.of.the.Living.Dead.1968.IMAX.2160p.mkv", "Night of the Living Dead", 1968},
		{"Nosferatu 1922 1080p.mkv", "Nosferatu", 1922},
	}
	for _, c := range cases {
		got := ParseMovie(c.in)
		if got.Title != c.title || got.Year != c.year {
			t.Errorf("ParseMovie(%q) = {%q, %d}, want {%q, %d}", c.in, got.Title, got.Year, c.title, c.year)
		}
	}
}

func TestParseFolder(t *testing.T) {
	cases := []struct {
		in    string
		title string
		year  int
		ok    bool
	}{
		{"Big Test Movie (2021)", "Big Test Movie", 2021, true},
		{"Test Show (2020)", "Test Show", 2020, true},
		{"Războiul Independenței (1912)", "Războiul Independenței", 1912, true},
		{"Movie (2010) [imdbid-tt123]", "Movie", 2010, true},
		{"Movies", "", 0, false},
		{"(2020)", "", 0, false},
		{"Old (1800)", "", 0, false},
	}
	for _, c := range cases {
		got, ok := ParseFolder(c.in)
		if ok != c.ok || got.Title != c.title || got.Year != c.year {
			t.Errorf("ParseFolder(%q) = {%q, %d}, %v; want {%q, %d}, %v", c.in, got.Title, got.Year, ok, c.title, c.year, c.ok)
		}
	}
}

func TestParseShowFolder(t *testing.T) {
	cases := []struct {
		in    string
		title string
		year  int
	}{
		{"Test Show (2020)", "Test Show", 2020},
		{"Bonanza", "Bonanza", 0},
		{"Dragnet.US.1951.1080p", "Dragnet US", 1951},
		{"Povești din Țară (2012)", "Povești din Țară", 2012},
	}
	for _, c := range cases {
		got := ParseShowFolder(c.in)
		if got.Title != c.title || got.Year != c.year {
			t.Errorf("ParseShowFolder(%q) = {%q, %d}, want {%q, %d}", c.in, got.Title, got.Year, c.title, c.year)
		}
	}
}

func TestParseSeasonFolder(t *testing.T) {
	cases := []struct {
		in string
		n  int
		ok bool
	}{
		{"Season 01", 1, true},
		{"Season 1", 1, true},
		{"season 12", 12, true},
		{"Season.03", 3, true},
		{"S01", 1, true},
		{"s2", 2, true},
		{"Specials", 0, true},
		{"Special", 0, true},
		{"Season 00", 0, true},
		{"Sezonul 2", 2, true},
		{"Season 2 (2021)", 2, true},
		{"Extras", 0, false},
		{"Seasoning", 0, false},
	}
	for _, c := range cases {
		n, ok := ParseSeasonFolder(c.in)
		if n != c.n || ok != c.ok {
			t.Errorf("ParseSeasonFolder(%q) = %d, %v; want %d, %v", c.in, n, ok, c.n, c.ok)
		}
	}
}

func TestParseEpisode(t *testing.T) {
	cases := []struct {
		in        string
		season    int
		hasSeason bool
		ep, end   int
		title     string
		ok        bool
	}{
		{"Test Show - S01E01 - Pilot.mkv", 1, true, 1, 0, "Pilot", true},
		{"Test Show - S01E02 - Second.mkv", 1, true, 2, 0, "Second", true},
		{"Test Show - S01E03E04 - Double.mkv", 1, true, 3, 4, "Double", true},
		{"Test Show - S00E01 - Special.mp4", 0, true, 1, 0, "Special", true},
		{"show.s01e02.mkv", 1, true, 2, 0, "", true},
		{"Show S01E02-E03 Title.mkv", 1, true, 2, 3, "Title", true},
		{"Show.S02E05-06.Two.Parter.mkv", 2, true, 5, 6, "Two Parter", true},
		{"Show S01E01E02E03.mkv", 1, true, 1, 3, "", true},
		{"Show.S10E100.mkv", 10, true, 100, 0, "", true},
		{"Show.Name.S03E07.The.Title.1080p.WEB.h264-GRP.mkv", 3, true, 7, 0, "The Title", true},
		{"Show.Name.S01E01.720p.HDTV.x264.mkv", 1, true, 1, 0, "", true},
		{"Show - 1x02 - The Return.mkv", 1, true, 2, 0, "The Return", true},
		{"show 2x10.avi", 2, true, 10, 0, "", true},
		{"Show 1x01-1x02 Double.mkv", 1, true, 1, 2, "Double", true},
		{"Show - E02 - Title.mkv", 0, false, 2, 0, "Title", true},
		{"Show Ep 02.mkv", 0, false, 2, 0, "", true},
		{"Show Ep.05 Something.mkv", 0, false, 5, 0, "Something", true},
		{"Episode 3.mp4", 0, false, 3, 0, "", true},
		{"Povești din Țară - S01E01 - Început.mkv", 1, true, 1, 0, "Început", true},
		{"Povesti.din.Tara.S05E12.Nuntă.la.țară.mkv", 5, true, 12, 0, "Nuntă la țară", true},
		{"Show - S01E05 - [Bonus] Finale.mkv", 1, true, 5, 0, "Finale", true},
		{"Show.1920x1080.mkv", 0, false, 0, 0, "", false},
		{"random clip.mkv", 0, false, 0, 0, "", false},
		{"Behind the Scenes.mkv", 0, false, 0, 0, "", false},
	}
	for _, c := range cases {
		got, ok := ParseEpisode(c.in)
		if ok != c.ok {
			t.Errorf("ParseEpisode(%q) ok = %v, want %v (%+v)", c.in, ok, c.ok, got)
			continue
		}
		if !ok {
			continue
		}
		if got.Season != c.season || got.HasSeason != c.hasSeason || got.Episode != c.ep || got.EpisodeEnd != c.end || got.Title != c.title {
			t.Errorf("ParseEpisode(%q) = %+v, want season=%d has=%v ep=%d end=%d title=%q",
				c.in, got, c.season, c.hasSeason, c.ep, c.end, c.title)
		}
	}
}

func TestIgnoreRules(t *testing.T) {
	for _, d := range []string{"Extras", "featurettes", "Behind The Scenes", "Deleted Scenes", "Interviews",
		"Trailers", "Samples", "Sample", "Other", "$RECYCLE.BIN", "System Volume Information", "@eaDir", ".trickplay", ".git"} {
		if !IgnoredDir(d) {
			t.Errorf("IgnoredDir(%q) = false", d)
		}
	}
	for _, d := range []string{"Season 01", "Movies", "Other People (2020)"} {
		if IgnoredDir(d) {
			t.Errorf("IgnoredDir(%q) = true", d)
		}
	}
	samples := []struct {
		name string
		size int64
		want bool
	}{
		{"movie-sample.mkv", 50 << 20, true},
		{"Movie.2020.SAMPLE.mkv", 10 << 20, true},
		{"sample.mkv", 1 << 20, true},
		{"movie-sample.mkv", 400 << 20, false},
		{"Free Samples (2012).mkv", 10 << 20, false},
		{"Sampler.mkv", 1 << 20, false},
		{"HDR Sample (2020).mkv", 30 << 20, false},
		{"sample-movie.2020.mkv", 30 << 20, true},
	}
	for _, s := range samples {
		if got := IsSample(s.name, s.size); got != s.want {
			t.Errorf("IsSample(%q, %d) = %v, want %v", s.name, s.size, got, s.want)
		}
	}
	for _, f := range []string{"a.mkv", "b.MP4", "c.m4v", "d.mov", "e.avi", "f.ts", "g.m2ts", "h.webm", "i.wmv"} {
		if !IsVideo(f) {
			t.Errorf("IsVideo(%q) = false", f)
		}
	}
	for _, f := range []string{"a.srt", "b.nfo", "c.jpg", "mkv"} {
		if IsVideo(f) {
			t.Errorf("IsVideo(%q) = true", f)
		}
	}
}

func TestParseLooseEpisode(t *testing.T) {
	cases := []struct {
		in      string
		ok      bool
		show    string
		year    int
		season  int
		episode int
		epTitle string
	}{
		{"Pioneer.One.S02E02.Fallen.1080p.AMZN.WEB-DL.DDP5.1.H.264-GRP.mkv", true, "Pioneer One", 0, 2, 2, "Fallen"},
		{"The.Beverly.Hillbillies.S01E01.The.Clampetts.Strike.Oil.1080p.HMAX.WEB-DL.mkv", true, "The Beverly Hillbillies", 0, 1, 1, "The Clampetts Strike Oil"},
		{"Bonanza.S10E19.1080p.WEB-DL.AAC2.0.H.264.mkv", true, "Bonanza", 0, 10, 19, ""},
		{"Dragnet (US) 1951 S03E07.mkv", true, "Dragnet (US)", 1951, 3, 7, ""},
		{"Show - 1x02 - Pilot.mkv", true, "Show", 0, 1, 2, "Pilot"},
		{"S01E02.mkv", true, "", 0, 1, 2, ""},
		// Not episodes in a movies library
		{"Space Saga Episode IV A New Dawn (1977).mkv", false, "", 0, 0, 0, ""},
		{"Rocky Episode 2.mkv", false, "", 0, 0, 0, ""},
		{"Tears.of.Steel.2012.1080p.BluRay.x264.mkv", false, "", 0, 0, 0, ""},
		{"Video 1920x1080.mkv", false, "", 0, 0, 0, ""},
	}
	for _, c := range cases {
		le, ok := ParseLooseEpisode(c.in)
		if ok != c.ok {
			t.Errorf("%q: ok=%v, want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if le.Show.Title != c.show || le.Show.Year != c.year || le.Season != c.season || le.Episode.Episode != c.episode || le.Title != c.epTitle {
			t.Errorf("%q: got show %q (%d) S%dE%d %q", c.in, le.Show.Title, le.Show.Year, le.Season, le.Episode.Episode, le.Title)
		}
	}
}

func TestParseSeasonPack(t *testing.T) {
	cases := []struct {
		in     string
		ok     bool
		show   string
		season int
	}{
		{"The.Beverly.Hillbillies.S01.1080p.HMAX.WEB-DL.DDP5.1.H.264-GRP", true, "The Beverly Hillbillies", 1},
		{"The.Lucy.Show.S03.1080p.WEB", true, "The Lucy Show", 3},
		{"Bonanza Season 2", true, "Bonanza", 2},
		{"Povesti din Tara Sezonul 12", true, "Povesti din Tara", 12},
		{"Season 1", false, "", 0},
		{"S01", false, "", 0},
		{"Tears.of.Steel.2012.1080p.BluRay", false, "", 0},
		{"Movies", false, "", 0},
	}
	for _, c := range cases {
		m, season, ok := ParseSeasonPack(c.in)
		if ok != c.ok || m.Title != c.show || season != c.season {
			t.Errorf("%q: got %q S%d ok=%v, want %q S%d ok=%v", c.in, m.Title, season, ok, c.show, c.season, c.ok)
		}
	}
}
