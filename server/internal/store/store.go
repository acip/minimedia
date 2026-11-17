// Package store is the SQLite persistence layer (PLAN.md section 3.1).
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "modernc.org/sqlite"

	"github.com/acip/minimedia/server/internal/progress"
)

const schemaVersion = 2

const schema = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE IF NOT EXISTS items (
  id               TEXT PRIMARY KEY,
  library          TEXT NOT NULL,
  kind             TEXT NOT NULL,            -- movie | episode
  path             TEXT NOT NULL,
  rel_path         TEXT NOT NULL,
  size             INTEGER NOT NULL,
  mtime            INTEGER NOT NULL,         -- unix ms
  title            TEXT NOT NULL,
  sort_title       TEXT NOT NULL,
  year             INTEGER NOT NULL DEFAULT 0,
  show_id          TEXT,
  season           INTEGER,
  episode          INTEGER,
  episode_end      INTEGER,
  overview         TEXT NOT NULL DEFAULT '',
  runtime_ms       INTEGER NOT NULL DEFAULT 0,
  added_at         INTEGER NOT NULL,         -- unix ms
  probe_json       TEXT NOT NULL DEFAULT '',
  tmdb_id          INTEGER,
  rating           REAL,
  genres           TEXT NOT NULL DEFAULT '[]',
  keyframes_cached INTEGER NOT NULL DEFAULT 0,
  trickplay_state  TEXT NOT NULL DEFAULT 'none'
);
CREATE INDEX IF NOT EXISTS items_library ON items(library);
CREATE INDEX IF NOT EXISTS items_kind_added ON items(kind, added_at);
CREATE INDEX IF NOT EXISTS items_show ON items(show_id, season, episode);

CREATE TABLE IF NOT EXISTS shows (
  id         TEXT PRIMARY KEY,
  library    TEXT NOT NULL,
  path       TEXT NOT NULL,
  title      TEXT NOT NULL,
  sort_title TEXT NOT NULL,
  year       INTEGER NOT NULL DEFAULT 0,
  overview   TEXT NOT NULL DEFAULT '',
  tmdb_id    INTEGER,
  genres     TEXT NOT NULL DEFAULT '[]',
  added_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS subtitles (
  id           TEXT PRIMARY KEY,
  item_id      TEXT NOT NULL,
  source       TEXT NOT NULL,                -- embedded | external
  stream_index INTEGER,
  path         TEXT,
  language     TEXT NOT NULL,
  label        TEXT NOT NULL,
  forced       INTEGER NOT NULL DEFAULT 0,
  sdh          INTEGER NOT NULL DEFAULT 0,
  codec        TEXT NOT NULL,
  ord          INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS subtitles_item ON subtitles(item_id);

CREATE TABLE IF NOT EXISTS images (
  owner_id    TEXT NOT NULL,
  kind        TEXT NOT NULL,                 -- poster | backdrop | logo | still
  source      TEXT NOT NULL,                 -- local | tmdb | frame
  path        TEXT NOT NULL,
  cached_path TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (owner_id, kind)
);

CREATE TABLE IF NOT EXISTS progress (
  item_id        TEXT PRIMARY KEY,
  position_ms    INTEGER NOT NULL DEFAULT 0,
  duration_ms    INTEGER NOT NULL DEFAULT 0,
  watched        INTEGER NOT NULL DEFAULT 0,
  play_count     INTEGER NOT NULL DEFAULT 0,
  last_played_at INTEGER NOT NULL DEFAULT 0, -- unix ms
  orphaned_at    INTEGER                     -- set when the file disappears; purged after 30 days
);

CREATE TABLE IF NOT EXISTS show_prefs (
  show_id            TEXT PRIMARY KEY,
  audio_language     TEXT NOT NULL DEFAULT '',
  subtitle_language  TEXT NOT NULL DEFAULT '',
  subtitles_mode     TEXT NOT NULL DEFAULT 'off'
);

CREATE TABLE IF NOT EXISTS markers (
  item_id  TEXT NOT NULL,
  kind     TEXT NOT NULL,                    -- intro | recap | credits
  start_ms INTEGER NOT NULL,
  end_ms   INTEGER NOT NULL,
  source   TEXT NOT NULL DEFAULT 'chapter'
);
CREATE INDEX IF NOT EXISTS markers_item ON markers(item_id);
`

// migrations[n] upgrades schema version n+1 to n+2. The base schema above is
// version 1, so a fresh database runs every migration.
var migrations = []string{
	// 2: TMDB metadata kept apart from what the scanner parses from file names,
	// tmdb.txt overrides, and images with one row per (owner, kind, source) so a
	// local file wins over TMDB, and TMDB takes over again if the file goes away.
	`ALTER TABLE items ADD COLUMN meta_title TEXT NOT NULL DEFAULT '';
	ALTER TABLE items ADD COLUMN meta_sort  TEXT NOT NULL DEFAULT '';
	ALTER TABLE items ADD COLUMN meta_year  INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE items ADD COLUMN meta_at    INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE items ADD COLUMN tmdb_hint  INTEGER;
	ALTER TABLE shows ADD COLUMN meta_title TEXT NOT NULL DEFAULT '';
	ALTER TABLE shows ADD COLUMN meta_sort  TEXT NOT NULL DEFAULT '';
	ALTER TABLE shows ADD COLUMN meta_year  INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE shows ADD COLUMN meta_at    INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE shows ADD COLUMN tmdb_hint  INTEGER;
	ALTER TABLE shows ADD COLUMN rating     REAL;
	DROP TABLE images;
	CREATE TABLE images (
	  owner_id TEXT NOT NULL,
	  kind     TEXT NOT NULL,                    -- poster | backdrop | logo | still
	  source   TEXT NOT NULL,                    -- local | tmdb (frame grabs are implicit)
	  path     TEXT NOT NULL,                    -- file path, or TMDB file_path
	  version  TEXT NOT NULL,                    -- changes with the image (URL cache-buster)
	  PRIMARY KEY (owner_id, kind, source)
	);`,
}

// OrphanRetention is how long progress is kept for files that disappeared.
const OrphanRetention = 30 * 24 * time.Hour

// ErrNotFound is returned when a row doesn't exist.
var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database in WAL mode.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	q := url.Values{}
	for _, p := range []string{"busy_timeout(10000)", "journal_mode(WAL)", "synchronous(NORMAL)", "foreign_keys(ON)"} {
		q.Add("_pragma", p)
	}
	// modernc.org/sqlite takes "file:" URIs; ToSlash keeps Windows drive paths valid.
	dsn := "file:" + (&url.URL{Path: filepath.ToSlash(path)}).EscapedPath() + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	v, err := s.Meta("schema_version")
	if err != nil {
		return err
	}
	cur := 1 // a fresh database has just been given the version-1 base schema
	if v != "" {
		if cur, err = strconv.Atoi(v); err != nil {
			return fmt.Errorf("bad schema_version %q", v)
		}
	}
	if cur > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than this server supports (%d)", cur, schemaVersion)
	}
	for ; cur < schemaVersion; cur++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[cur-1]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrate database to version %d: %w", cur+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES('schema_version', ?)
		  ON CONFLICT(key) DO UPDATE SET value = excluded.value`, strconv.Itoa(cur+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if v == "" {
		return s.SetMeta("schema_version", strconv.Itoa(schemaVersion))
	}
	return nil
}

func (s *Store) Meta(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// ServerID returns a random id generated on first use and kept forever.
func (s *Store) ServerID() (string, error) {
	id, err := s.Meta("server_id")
	if err != nil || id != "" {
		return id, err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id = hex.EncodeToString(b)
	return id, s.SetMeta("server_id", id)
}

// ---- items -------------------------------------------------------------------

type Item struct {
	ID         string
	Library    string
	Kind       string // movie | episode
	Path       string
	RelPath    string
	Size       int64
	MtimeMs    int64
	Title      string
	SortTitle  string
	Year       int
	ShowID     string
	Season     int
	Episode    int
	EpisodeEnd int
	Overview   string
	RuntimeMs  int64
	AddedAt    int64
	ProbeJSON  string
	Genres     []string
	Rating     float64
	TMDBHint   int64 // id from a tmdb.txt next to the file (scanner-owned)

	// Read-only
	FileTitle string // the title parsed from the file name (Title prefers TMDB's)
	TMDBID    int64
	ShowTitle string
	Progress  progress.State
}

// Display values prefer TMDB metadata over what the scanner parsed.
const (
	itemTitle = `COALESCE(NULLIF(i.meta_title, ''), i.title)`
	itemSort  = `COALESCE(NULLIF(i.meta_sort, ''), i.sort_title)`
	itemYear  = `CASE WHEN i.year > 0 THEN i.year ELSE i.meta_year END`
	showTitle = `COALESCE(NULLIF(s.meta_title, ''), s.title)`
	showSort  = `COALESCE(NULLIF(s.meta_sort, ''), s.sort_title)`
	showYear  = `CASE WHEN s.year > 0 THEN s.year ELSE s.meta_year END`
)

const itemCols = `i.id, i.library, i.kind, i.path, i.rel_path, i.size, i.mtime, ` + itemTitle + `, ` + itemSort + `, ` + itemYear + `,
  COALESCE(i.show_id, ''), COALESCE(i.season, 0), COALESCE(i.episode, 0), COALESCE(i.episode_end, 0),
  i.overview, i.runtime_ms, i.added_at, i.genres, COALESCE(i.rating, 0),
  COALESCE(NULLIF(sh.meta_title, ''), sh.title, ''),
  COALESCE(p.position_ms, 0), COALESCE(p.duration_ms, 0), COALESCE(p.watched, 0), COALESCE(p.play_count, 0), COALESCE(p.last_played_at, 0),
  i.title, COALESCE(i.tmdb_id, 0), COALESCE(i.tmdb_hint, 0)`

const itemFrom = ` FROM items i LEFT JOIN shows sh ON sh.id = i.show_id LEFT JOIN progress p ON p.item_id = i.id `

type scanner interface{ Scan(...any) error }

func scanItem(r scanner, withProbe bool) (*Item, error) {
	var it Item
	var genres string
	var watched int
	var last int64
	dest := []any{&it.ID, &it.Library, &it.Kind, &it.Path, &it.RelPath, &it.Size, &it.MtimeMs, &it.Title, &it.SortTitle, &it.Year,
		&it.ShowID, &it.Season, &it.Episode, &it.EpisodeEnd, &it.Overview, &it.RuntimeMs, &it.AddedAt, &genres, &it.Rating,
		&it.ShowTitle, &it.Progress.PositionMs, &it.Progress.DurationMs, &watched, &it.Progress.PlayCount, &last,
		&it.FileTitle, &it.TMDBID, &it.TMDBHint}
	if withProbe {
		dest = append(dest, &it.ProbeJSON)
	}
	if err := r.Scan(dest...); err != nil {
		return nil, err
	}
	it.Progress.Watched = watched != 0
	if last > 0 {
		it.Progress.LastPlayedAt = time.UnixMilli(last)
	}
	_ = json.Unmarshal([]byte(genres), &it.Genres)
	return &it, nil
}

func (s *Store) queryItems(ctx context.Context, where string, args ...any) ([]*Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemCols+itemFrom+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		it, err := scanItem(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// GetItem returns one item including its probe JSON.
func (s *Store) GetItem(ctx context.Context, id string) (*Item, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+itemCols+`, i.probe_json`+itemFrom+`WHERE i.id = ?`, id)
	it, err := scanItem(row, true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return it, err
}

// FileState is what the scanner needs to decide whether to re-probe.
type FileState struct {
	Size    int64
	MtimeMs int64
	Probed  bool
	RelPath string
	Kind    string
	ShowID  string
	AddedAt int64
}

// LibraryFiles returns the stored file state of every item in a library.
func (s *Store) LibraryFiles(ctx context.Context, library string) (map[string]FileState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, size, mtime, probe_json != '', rel_path, kind, COALESCE(show_id, ''), added_at FROM items WHERE library = ?`, library)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]FileState{}
	for rows.Next() {
		var id string
		var fs FileState
		if err := rows.Scan(&id, &fs.Size, &fs.MtimeMs, &fs.Probed, &fs.RelPath, &fs.Kind, &fs.ShowID, &fs.AddedAt); err != nil {
			return nil, err
		}
		out[id] = fs
	}
	return out, rows.Err()
}

// Tx wraps a write transaction for the scanner.
type Tx struct{ tx *sql.Tx }

func (s *Store) Begin(ctx context.Context) (*Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Tx{tx: tx}, nil
}

func (t *Tx) Commit() error   { return t.tx.Commit() }
func (t *Tx) Rollback() error { return t.tx.Rollback() }

func nullInt(kind string, v int) any {
	if kind != "episode" {
		return nil
	}
	return v
}

// UpsertItem inserts or updates an item. added_at is kept from the first insert;
// probe_json is only replaced when it.ProbeJSON is non-empty.
func (t *Tx) UpsertItem(it *Item) error {
	genres, _ := json.Marshal(nonNil(it.Genres))
	var showID any
	if it.ShowID != "" {
		showID = it.ShowID
	}
	var epEnd any
	if it.EpisodeEnd > 0 {
		epEnd = it.EpisodeEnd
	}
	// A file that turned from a movie into an episode (or back) loses the
	// metadata and TMDB pictures of its old kind.
	var oldKind string
	err := t.tx.QueryRow(`SELECT kind FROM items WHERE id = ?`, it.ID).Scan(&oldKind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if oldKind != "" && oldKind != it.Kind {
		if _, err := t.tx.Exec(`UPDATE items SET tmdb_id = NULL, meta_title = '', meta_sort = '', meta_year = 0,
		  overview = '', genres = '[]', rating = NULL, meta_at = 0 WHERE id = ?`, it.ID); err != nil {
			return err
		}
		if _, err := t.tx.Exec(`DELETE FROM images WHERE owner_id = ? AND source = 'tmdb'`, it.ID); err != nil {
			return err
		}
	}
	// A changed tmdb.txt makes the metadata fetcher look the item up again.
	_, err = t.tx.Exec(`INSERT INTO items (id, library, kind, path, rel_path, size, mtime, title, sort_title, year,
	    show_id, season, episode, episode_end, runtime_ms, added_at, probe_json, genres, tmdb_hint)
	  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	  ON CONFLICT(id) DO UPDATE SET
	    meta_at = CASE WHEN COALESCE(excluded.tmdb_hint, 0) != COALESCE(items.tmdb_hint, 0) THEN 0 ELSE items.meta_at END,
	    tmdb_hint = excluded.tmdb_hint,
	    library = excluded.library, kind = excluded.kind, path = excluded.path, rel_path = excluded.rel_path,
	    size = excluded.size, mtime = excluded.mtime, title = excluded.title, sort_title = excluded.sort_title,
	    year = excluded.year, show_id = excluded.show_id, season = excluded.season, episode = excluded.episode,
	    episode_end = excluded.episode_end,
	    runtime_ms = CASE WHEN excluded.probe_json != '' THEN excluded.runtime_ms ELSE items.runtime_ms END,
	    probe_json = CASE WHEN excluded.probe_json != '' THEN excluded.probe_json ELSE items.probe_json END`,
		it.ID, it.Library, it.Kind, it.Path, it.RelPath, it.Size, it.MtimeMs, it.Title, it.SortTitle, it.Year,
		showID, nullInt(it.Kind, it.Season), nullInt(it.Kind, it.Episode), epEnd, it.RuntimeMs, it.AddedAt, it.ProbeJSON, string(genres),
		nullID(it.TMDBHint))
	if err != nil {
		return fmt.Errorf("upsert item %s: %w", it.RelPath, err)
	}
	// A file that came back gets its progress back.
	_, err = t.tx.Exec(`UPDATE progress SET orphaned_at = NULL WHERE item_id = ? AND orphaned_at IS NOT NULL`, it.ID)
	return err
}

// DeleteItems removes items and their subtitles/markers. Progress is kept and
// marked orphaned so it survives a temporarily missing file.
func (t *Tx) DeleteItems(ids []string, now time.Time) error {
	for _, id := range ids {
		for _, q := range []string{
			`DELETE FROM items WHERE id = ?`,
			`DELETE FROM subtitles WHERE item_id = ?`,
			`DELETE FROM markers WHERE item_id = ?`,
			`DELETE FROM images WHERE owner_id = ?`,
		} {
			if _, err := t.tx.Exec(q, id); err != nil {
				return err
			}
		}
		if _, err := t.tx.Exec(`UPDATE progress SET orphaned_at = ? WHERE item_id = ? AND orphaned_at IS NULL`, now.UnixMilli(), id); err != nil {
			return err
		}
	}
	return nil
}

// PurgeOrphans deletes progress of files missing for longer than OrphanRetention
// and shows without episodes.
func (s *Store) PurgeOrphans(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM progress WHERE orphaned_at IS NOT NULL AND orphaned_at < ?`,
		now.Add(-OrphanRetention).UnixMilli()); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM show_prefs WHERE show_id NOT IN (SELECT id FROM shows)`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM shows WHERE id NOT IN (SELECT DISTINCT show_id FROM items WHERE show_id IS NOT NULL)`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM images WHERE owner_id NOT IN (SELECT id FROM items UNION ALL SELECT id FROM shows)`)
	return err
}

// ---- subtitles & markers -----------------------------------------------------

type Subtitle struct {
	ID          string
	ItemID      string
	Source      string // embedded | external
	StreamIndex int
	Path        string
	Language    string
	Label       string
	Forced      bool
	SDH         bool
	Codec       string
}

// ReplaceSubtitles replaces an item's subtitles of one source (embedded or
// external), keeping the other source. Embedded tracks sort before external ones.
func (t *Tx) ReplaceSubtitles(itemID, source string, list []Subtitle) error {
	if _, err := t.tx.Exec(`DELETE FROM subtitles WHERE item_id = ? AND source = ?`, itemID, source); err != nil {
		return err
	}
	base := 0
	if source == "external" {
		base = 10000
	}
	for i, s := range list {
		var idx, path any
		if source == "embedded" {
			idx = s.StreamIndex
		} else {
			path = s.Path
		}
		if _, err := t.tx.Exec(`INSERT INTO subtitles (id, item_id, source, stream_index, path, language, label, forced, sdh, codec, ord)
		  VALUES (?,?,?,?,?,?,?,?,?,?,?)`, s.ID, itemID, source, idx, path, s.Language, s.Label, s.Forced, s.SDH, s.Codec, base+i); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Subtitles(ctx context.Context, itemID string) ([]Subtitle, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, item_id, source, COALESCE(stream_index, -1), COALESCE(path, ''), language, label, forced, sdh, codec
	  FROM subtitles WHERE item_id = ? ORDER BY ord`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subtitle
	for rows.Next() {
		var x Subtitle
		if err := rows.Scan(&x.ID, &x.ItemID, &x.Source, &x.StreamIndex, &x.Path, &x.Language, &x.Label, &x.Forced, &x.SDH, &x.Codec); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) Subtitle(ctx context.Context, itemID, subID string) (*Subtitle, error) {
	list, err := s.Subtitles(ctx, itemID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == subID {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}

type Marker struct {
	Kind    string
	StartMs int64
	EndMs   int64
}

func (t *Tx) ReplaceMarkers(itemID string, list []Marker) error {
	if _, err := t.tx.Exec(`DELETE FROM markers WHERE item_id = ? AND source = 'chapter'`, itemID); err != nil {
		return err
	}
	for _, m := range list {
		if _, err := t.tx.Exec(`INSERT INTO markers (item_id, kind, start_ms, end_ms, source) VALUES (?,?,?,?, 'chapter')`,
			itemID, m.Kind, m.StartMs, m.EndMs); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Markers(ctx context.Context, itemID string) ([]Marker, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, start_ms, end_ms FROM markers WHERE item_id = ? ORDER BY start_ms`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Marker
	for rows.Next() {
		var m Marker
		if err := rows.Scan(&m.Kind, &m.StartMs, &m.EndMs); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CreditsStart returns the start of the item's credits marker, or 0.
func (s *Store) CreditsStart(ctx context.Context, itemID string) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `SELECT start_ms FROM markers WHERE item_id = ? AND kind = 'credits' ORDER BY start_ms LIMIT 1`, itemID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// ---- shows -------------------------------------------------------------------

type Show struct {
	ID        string
	Library   string
	Path      string
	Title     string
	SortTitle string
	Year      int
	Overview  string
	Genres    []string
	AddedAt   int64
	TMDBHint  int64 // id from tmdb.txt in the show folder (scanner-owned)

	// Read-only
	FileTitle    string
	TMDBID       int64
	Rating       float64
	EpisodeCount int // regular episodes (specials excluded)
	WatchedCount int
	SeasonCount  int
	LastAddedAt  int64
	LastPlayedAt int64
}

func (t *Tx) UpsertShow(sh *Show) error {
	genres, _ := json.Marshal(nonNil(sh.Genres))
	_, err := t.tx.Exec(`INSERT INTO shows (id, library, path, title, sort_title, year, genres, added_at, tmdb_hint) VALUES (?,?,?,?,?,?,?,?,?)
	  ON CONFLICT(id) DO UPDATE SET library = excluded.library, path = excluded.path, title = excluded.title,
	    sort_title = excluded.sort_title, year = excluded.year,
	    meta_at = CASE WHEN COALESCE(excluded.tmdb_hint, 0) != COALESCE(shows.tmdb_hint, 0) THEN 0 ELSE shows.meta_at END,
	    tmdb_hint = excluded.tmdb_hint`,
		sh.ID, sh.Library, sh.Path, sh.Title, sh.SortTitle, sh.Year, string(genres), sh.AddedAt, nullID(sh.TMDBHint))
	return err
}

const showCols = `s.id, s.library, s.path, ` + showTitle + `, ` + showSort + `, ` + showYear + `, s.overview, s.genres, s.added_at,
  s.title, COALESCE(s.tmdb_id, 0), COALESCE(s.tmdb_hint, 0), COALESCE(s.rating, 0),
  (SELECT COUNT(*) FROM items e WHERE e.show_id = s.id AND e.season > 0),
  (SELECT COUNT(*) FROM items e JOIN progress p ON p.item_id = e.id WHERE e.show_id = s.id AND e.season > 0 AND p.watched = 1),
  (SELECT COUNT(DISTINCT e.season) FROM items e WHERE e.show_id = s.id AND e.season > 0),
  (SELECT COALESCE(MAX(e.added_at), 0) FROM items e WHERE e.show_id = s.id),
  (SELECT COALESCE(MAX(p.last_played_at), 0) FROM items e JOIN progress p ON p.item_id = e.id WHERE e.show_id = s.id)`

func scanShow(r scanner) (*Show, error) {
	var sh Show
	var genres string
	if err := r.Scan(&sh.ID, &sh.Library, &sh.Path, &sh.Title, &sh.SortTitle, &sh.Year, &sh.Overview, &genres, &sh.AddedAt,
		&sh.FileTitle, &sh.TMDBID, &sh.TMDBHint, &sh.Rating, &sh.EpisodeCount, &sh.WatchedCount, &sh.SeasonCount, &sh.LastAddedAt, &sh.LastPlayedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(genres), &sh.Genres)
	return &sh, nil
}

func (s *Store) GetShow(ctx context.Context, id string) (*Show, error) {
	sh, err := scanShow(s.db.QueryRowContext(ctx, `SELECT `+showCols+` FROM shows s WHERE s.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sh, err
}

// ListShows returns all shows. sort: title | added | year.
func (s *Store) ListShows(ctx context.Context, sort string, limit int) ([]*Show, error) {
	q := `SELECT ` + showCols + ` FROM shows s ORDER BY ` + showOrder(sort)
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Show
	for rows.Next() {
		sh, err := scanShow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

func showOrder(sort string) string {
	switch sort {
	case "added":
		return "(SELECT COALESCE(MAX(e.added_at), 0) FROM items e WHERE e.show_id = s.id) DESC, " + showSort
	case "year":
		return showYear + " DESC, " + showSort
	}
	return showSort + ", " + showYear
}

// Episodes returns a show's episodes ordered by season (specials last) and episode.
func (s *Store) Episodes(ctx context.Context, showID string) ([]*Item, error) {
	return s.queryItems(ctx, `WHERE i.show_id = ? ORDER BY CASE WHEN i.season = 0 THEN 1 ELSE 0 END, i.season, i.episode, i.sort_title`, showID)
}

// ---- lists for the API -------------------------------------------------------

// ListMovies returns movies. sort: title | added | year. genre filters (case-insensitive).
func (s *Store) ListMovies(ctx context.Context, sort, genre string, limit int) ([]*Item, error) {
	where := `WHERE i.kind = 'movie' `
	var args []any
	if genre != "" {
		where += `AND EXISTS (SELECT 1 FROM json_each(i.genres) g WHERE lower(g.value) = lower(?)) `
		args = append(args, genre)
	}
	switch sort {
	case "added":
		where += `ORDER BY i.added_at DESC, ` + itemSort
	case "year":
		where += `ORDER BY ` + itemYear + ` DESC, ` + itemSort
	default:
		where += `ORDER BY ` + itemSort + `, ` + itemYear
	}
	if limit > 0 {
		where += fmt.Sprintf(" LIMIT %d", limit)
	}
	return s.queryItems(ctx, where, args...)
}

// RecentlyAdded returns unwatched items of a kind added after since, newest first.
func (s *Store) RecentlyAdded(ctx context.Context, kind string, since time.Time, limit int) ([]*Item, error) {
	return s.queryItems(ctx, `WHERE i.kind = ? AND i.added_at >= ? AND COALESCE(p.watched, 0) = 0
	  AND NOT (i.kind = 'episode' AND i.season = 0) ORDER BY i.added_at DESC, `+itemSort+` LIMIT ?`, kind, since.UnixMilli(), limit)
}

// ContinueWatching returns partially watched items, most recently played first.
func (s *Store) ContinueWatching(ctx context.Context, limit int) ([]*Item, error) {
	return s.queryItems(ctx, `WHERE p.position_ms > 0 ORDER BY p.last_played_at DESC LIMIT ?`, limit)
}

// ActiveShows returns ids of shows with any episode played since `since`.
func (s *Store) ActiveShows(ctx context.Context, since time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.show_id FROM items i JOIN progress p ON p.item_id = i.id
	  WHERE i.show_id IS NOT NULL AND p.last_played_at >= ? GROUP BY i.show_id ORDER BY MAX(p.last_played_at) DESC`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AllItems returns every item (for search). Probe JSON is not loaded.
func (s *Store) AllItems(ctx context.Context) ([]*Item, error) {
	return s.queryItems(ctx, `ORDER BY `+itemSort)
}

// ProbedItems returns every analysed item with its ffprobe JSON, newest first.
func (s *Store) ProbedItems(ctx context.Context) ([]*Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemCols+`, i.probe_json`+itemFrom+`WHERE i.probe_json != '' ORDER BY i.added_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		it, err := scanItem(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ---- progress ----------------------------------------------------------------

func (s *Store) Progress(ctx context.Context, itemID string) (progress.State, error) {
	var st progress.State
	var watched int
	var last int64
	err := s.db.QueryRowContext(ctx, `SELECT position_ms, duration_ms, watched, play_count, last_played_at FROM progress WHERE item_id = ?`, itemID).
		Scan(&st.PositionMs, &st.DurationMs, &watched, &st.PlayCount, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return progress.State{}, nil
	}
	st.Watched = watched != 0
	if last > 0 {
		st.LastPlayedAt = time.UnixMilli(last)
	}
	return st, err
}

// SetProgress overwrites an item's progress.
func (s *Store) SetProgress(ctx context.Context, itemID string, st progress.State) error {
	_, err := s.UpdateProgress(ctx, itemID, func(progress.State) progress.State { return st })
	return err
}

// UpdateProgress applies fn to an item's progress inside one transaction.
func (s *Store) UpdateProgress(ctx context.Context, itemID string, fn func(progress.State) progress.State) (progress.State, error) {
	// SQLite serialises writers; a read-modify-write in a single connection-level
	// transaction keeps concurrent reports from the app consistent.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return progress.State{}, err
	}
	defer tx.Rollback()
	var st progress.State
	var watched int
	var last int64
	err = tx.QueryRowContext(ctx, `SELECT position_ms, duration_ms, watched, play_count, last_played_at FROM progress WHERE item_id = ?`, itemID).
		Scan(&st.PositionMs, &st.DurationMs, &watched, &st.PlayCount, &last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return st, err
	}
	st.Watched = watched != 0
	if last > 0 {
		st.LastPlayedAt = time.UnixMilli(last)
	}
	st = fn(st)
	var lastOut int64
	if !st.LastPlayedAt.IsZero() {
		lastOut = st.LastPlayedAt.UnixMilli()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO progress (item_id, position_ms, duration_ms, watched, play_count, last_played_at)
	  VALUES (?,?,?,?,?,?) ON CONFLICT(item_id) DO UPDATE SET position_ms = excluded.position_ms, duration_ms = excluded.duration_ms,
	  watched = excluded.watched, play_count = excluded.play_count, last_played_at = excluded.last_played_at`,
		itemID, st.PositionMs, st.DurationMs, st.Watched, st.PlayCount, lastOut); err != nil {
		return st, err
	}
	return st, tx.Commit()
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
