package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ---- images ------------------------------------------------------------------

// Image is one image source for an owner (item or show). Frame grabs are not
// stored: they are the implicit last fallback for backdrops and stills.
type Image struct {
	OwnerID string
	Kind    string // poster | backdrop | logo | still
	Source  string // local | tmdb
	Path    string // absolute file path (local) or TMDB file_path ("/abc.jpg")
	Version string
}

// sourceRank orders sources by priority (PLAN.md 3.8: local → TMDB → frame).
const sourceRank = `CASE source WHEN 'local' THEN 0 WHEN 'tmdb' THEN 1 ELSE 2 END`

// ReplaceImages replaces an owner's images of one source.
func (t *Tx) ReplaceImages(ownerID, source string, list []Image) error {
	return replaceImages(t.tx, ownerID, source, list)
}

// ReplaceImages is the non-transactional variant used by the metadata fetcher.
func (s *Store) ReplaceImages(ctx context.Context, ownerID, source string, list []Image) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := replaceImages(tx, ownerID, source, list); err != nil {
		return err
	}
	return tx.Commit()
}

func replaceImages(tx *sql.Tx, ownerID, source string, list []Image) error {
	if _, err := tx.Exec(`DELETE FROM images WHERE owner_id = ? AND source = ?`, ownerID, source); err != nil {
		return err
	}
	for _, im := range list {
		if _, err := tx.Exec(`INSERT INTO images (owner_id, kind, source, path, version) VALUES (?,?,?,?,?)
		  ON CONFLICT(owner_id, kind, source) DO UPDATE SET path = excluded.path, version = excluded.version`,
			ownerID, im.Kind, source, im.Path, im.Version); err != nil {
			return err
		}
	}
	return nil
}

// BestImage returns the highest-priority stored image of a kind, or ErrNotFound.
func (s *Store) BestImage(ctx context.Context, ownerID, kind string) (*Image, error) {
	im := Image{OwnerID: ownerID, Kind: kind}
	err := s.db.QueryRowContext(ctx, `SELECT source, path, version FROM images WHERE owner_id = ? AND kind = ?
	  ORDER BY `+sourceRank+` LIMIT 1`, ownerID, kind).Scan(&im.Source, &im.Path, &im.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &im, nil
}

// ImageRefs returns, per owner, the best stored image of each kind.
func (s *Store) ImageRefs(ctx context.Context, ownerIDs []string) (map[string]map[string]Image, error) {
	out := map[string]map[string]Image{}
	seen := map[string]bool{}
	var ids []string
	for _, id := range ownerIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	const chunk = 400
	for len(ids) > 0 {
		n := min(chunk, len(ids))
		part := ids[:n]
		ids = ids[n:]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, `SELECT owner_id, kind, source, path, version FROM images
		  WHERE owner_id IN (?`+strings.Repeat(",?", len(part)-1)+`) ORDER BY owner_id, kind, `+sourceRank, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var im Image
			if err := rows.Scan(&im.OwnerID, &im.Kind, &im.Source, &im.Path, &im.Version); err != nil {
				rows.Close()
				return nil, err
			}
			m := out[im.OwnerID]
			if m == nil {
				m = map[string]Image{}
				out[im.OwnerID] = m
			}
			if _, ok := m[im.Kind]; !ok {
				m[im.Kind] = im
			}
		}
		err = rows.Err()
		if cerr := rows.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ---- show preferences --------------------------------------------------------

// ShowPrefs are the audio/subtitle choices remembered per show.
type ShowPrefs struct {
	AudioLanguage    string `json:"audioLanguage"`
	SubtitleLanguage string `json:"subtitleLanguage"`
	SubtitlesMode    string `json:"subtitlesMode"` // off | on | forced_only
	Set              bool   `json:"-"`             // false = never saved (automatic choices apply)
}

func (s *Store) ShowPrefs(ctx context.Context, showID string) (ShowPrefs, error) {
	p := ShowPrefs{SubtitlesMode: "off"}
	err := s.db.QueryRowContext(ctx, `SELECT audio_language, subtitle_language, subtitles_mode FROM show_prefs WHERE show_id = ?`, showID).
		Scan(&p.AudioLanguage, &p.SubtitleLanguage, &p.SubtitlesMode)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	p.Set = err == nil
	return p, err
}

func (s *Store) SetShowPrefs(ctx context.Context, showID string, p ShowPrefs) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO show_prefs (show_id, audio_language, subtitle_language, subtitles_mode) VALUES (?,?,?,?)
	  ON CONFLICT(show_id) DO UPDATE SET audio_language = excluded.audio_language,
	    subtitle_language = excluded.subtitle_language, subtitles_mode = excluded.subtitles_mode`,
		showID, p.AudioLanguage, p.SubtitleLanguage, p.SubtitlesMode)
	return err
}

// ---- metadata ----------------------------------------------------------------

// MetaTarget is a movie or show the metadata fetcher should look up.
type MetaTarget struct {
	ID       string
	Path     string
	Title    string // parsed from the file/folder name
	Year     int
	Hint     int64 // tmdb.txt
	TMDBID   int64 // current match, 0 = none
	Attempts bool  // looked up before (meta_at > 0)
}

// MoviesNeedingMeta returns movies never looked up, or unmatched ones last tried before retryBefore.
func (s *Store) MoviesNeedingMeta(ctx context.Context, retryBefore time.Time) ([]MetaTarget, error) {
	return s.metaTargets(ctx, `SELECT id, path, title, year, COALESCE(tmdb_hint, 0), COALESCE(tmdb_id, 0), meta_at > 0 FROM items
	  WHERE kind = 'movie' AND (meta_at = 0 OR (tmdb_id IS NULL AND meta_at < ?)) ORDER BY added_at DESC`, retryBefore.UnixMilli())
}

// ShowsNeedingMeta returns shows never looked up, unmatched ones last tried
// before retryBefore, and matched shows that have episodes not looked up yet.
func (s *Store) ShowsNeedingMeta(ctx context.Context, retryBefore time.Time) ([]MetaTarget, error) {
	return s.metaTargets(ctx, `SELECT s.id, s.path, s.title, s.year, COALESCE(s.tmdb_hint, 0), COALESCE(s.tmdb_id, 0), s.meta_at > 0 FROM shows s
	  WHERE s.meta_at = 0 OR (s.tmdb_id IS NULL AND s.meta_at < ?)
	     OR (s.tmdb_id IS NOT NULL AND EXISTS (SELECT 1 FROM items e WHERE e.show_id = s.id AND e.meta_at = 0))
	  ORDER BY s.added_at DESC`, retryBefore.UnixMilli())
}

func (s *Store) metaTargets(ctx context.Context, q string, args ...any) ([]MetaTarget, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MetaTarget
	for rows.Next() {
		var t MetaTarget
		if err := rows.Scan(&t.ID, &t.Path, &t.Title, &t.Year, &t.Hint, &t.TMDBID, &t.Attempts); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// EpisodeRef identifies an episode for metadata matching.
type EpisodeRef struct {
	ID         string
	Season     int
	Episode    int
	EpisodeEnd int
	Looked     bool // meta_at > 0
}

func (s *Store) EpisodeRefs(ctx context.Context, showID string) ([]EpisodeRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(season, 0), COALESCE(episode, 0), COALESCE(episode_end, 0), meta_at > 0
	  FROM items WHERE show_id = ? ORDER BY season, episode`, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EpisodeRef
	for rows.Next() {
		var e EpisodeRef
		if err := rows.Scan(&e.ID, &e.Season, &e.Episode, &e.EpisodeEnd, &e.Looked); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Meta is metadata fetched from TMDB.
type Meta struct {
	TMDBID   int64
	Title    string
	SortKey  string
	Year     int
	Overview string
	Genres   []string
	Rating   float64
}

// SetItemMeta stores metadata for a movie or episode. A nil m records a lookup
// that found nothing (the item keeps its file-name title).
func (s *Store) SetItemMeta(ctx context.Context, id string, m *Meta, now time.Time) error {
	if m == nil {
		_, err := s.db.ExecContext(ctx, `UPDATE items SET meta_at = ? WHERE id = ?`, now.UnixMilli(), id)
		return err
	}
	genres, _ := json.Marshal(nonNil(m.Genres))
	_, err := s.db.ExecContext(ctx, `UPDATE items SET tmdb_id = ?, meta_title = ?, meta_sort = ?, meta_year = ?, overview = ?,
	  genres = ?, rating = ?, meta_at = ? WHERE id = ?`,
		nullID(m.TMDBID), m.Title, m.SortKey, m.Year, m.Overview, string(genres), nullRating(m.Rating), now.UnixMilli(), id)
	return err
}

// SetShowMeta is SetItemMeta for shows.
func (s *Store) SetShowMeta(ctx context.Context, id string, m *Meta, now time.Time) error {
	if m == nil {
		_, err := s.db.ExecContext(ctx, `UPDATE shows SET meta_at = ? WHERE id = ?`, now.UnixMilli(), id)
		return err
	}
	genres, _ := json.Marshal(nonNil(m.Genres))
	_, err := s.db.ExecContext(ctx, `UPDATE shows SET tmdb_id = ?, meta_title = ?, meta_sort = ?, meta_year = ?, overview = ?,
	  genres = ?, rating = ?, meta_at = ? WHERE id = ?`,
		nullID(m.TMDBID), m.Title, m.SortKey, m.Year, m.Overview, string(genres), nullRating(m.Rating), now.UnixMilli(), id)
	return err
}

// ResetMetadata queues every movie, show and episode for a new lookup (e.g.
// after metadata_language changed). Existing metadata stays until replaced.
func (s *Store) ResetMetadata(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE items SET meta_at = 0`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE shows SET meta_at = 0`)
	return err
}

// MetaCounts summarises metadata coverage for the status endpoint.
type MetaCounts struct {
	Matched   int `json:"matched"`
	Unmatched int `json:"unmatched"`
	Pending   int `json:"pending"`
}

func (s *Store) MetaCounts(ctx context.Context) (MetaCounts, error) {
	var c MetaCounts
	err := s.db.QueryRowContext(ctx, `SELECT
	  (SELECT COUNT(*) FROM items WHERE kind = 'movie' AND tmdb_id IS NOT NULL) + (SELECT COUNT(*) FROM shows WHERE tmdb_id IS NOT NULL),
	  (SELECT COUNT(*) FROM items WHERE kind = 'movie' AND tmdb_id IS NULL AND meta_at > 0) + (SELECT COUNT(*) FROM shows WHERE tmdb_id IS NULL AND meta_at > 0),
	  (SELECT COUNT(*) FROM items WHERE kind = 'movie' AND meta_at = 0) + (SELECT COUNT(*) FROM shows WHERE meta_at = 0)`).
		Scan(&c.Matched, &c.Unmatched, &c.Pending)
	return c, err
}

// ---- trickplay ---------------------------------------------------------------

// TrickplayStates returns items.trickplay_state for every item that has one
// set ("ready:<version>" or "failed:<version>").
func (s *Store) TrickplayStates(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, trickplay_state FROM items WHERE trickplay_state NOT IN ('', 'none')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, st string
		if err := rows.Scan(&id, &st); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

func (s *Store) SetTrickplayState(ctx context.Context, id, state string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE items SET trickplay_state = ? WHERE id = ?`, state, id)
	return err
}

func nullID(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

func nullRating(v float64) any {
	if v <= 0 {
		return nil
	}
	return v
}
