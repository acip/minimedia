// Package metadata fetches titles, descriptions and artwork from TMDB
// (PLAN.md 3.7). It only runs when tmdb_api_key is set, in the background
// after scans, and never blocks the library.
package metadata

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is TMDB's 404.
var ErrNotFound = errors.New("not found on TMDB")

// ErrAuth means TMDB rejected the key.
var ErrAuth = errors.New("TMDB rejected the API key (check tmdb_api_key)")

// Client is a small TMDB v3 API client with a response cache and rate limit.
type Client struct {
	key    string
	bearer bool // v4 read access token instead of a v3 api_key
	Lang   string
	Base   string // https://api.themoviedb.org/3
	http   *http.Client
	cache  string        // folder for cached responses
	ttl    time.Duration // how long cached responses are used
	gap    time.Duration // minimum time between requests (~20/s)

	mu   sync.Mutex
	last time.Time
}

// NewClient detects the key type: v3 keys are 32 hex characters; v4 read
// access tokens are long JWTs ("eyJ…").
func NewClient(key, lang, cacheDir string) *Client {
	key = strings.TrimSpace(key)
	return &Client{
		key: key, bearer: len(key) > 40 || strings.HasPrefix(key, "eyJ"),
		Lang: lang, Base: "https://api.themoviedb.org/3",
		http:  &http.Client{Timeout: 30 * time.Second},
		cache: cacheDir, ttl: 7 * 24 * time.Hour, gap: 50 * time.Millisecond,
	}
}

// get fetches path?query into out. fresh skips the cache (the response is
// still written to it).
func (c *Client) get(ctx context.Context, p string, q url.Values, fresh bool, out any) error {
	if q == nil {
		q = url.Values{}
	}
	cacheKey := p + "?" + q.Encode() // without the key
	sum := sha1.Sum([]byte(cacheKey))
	cacheFile := filepath.Join(c.cache, hex.EncodeToString(sum[:])+".json")
	if !fresh {
		if fi, err := os.Stat(cacheFile); err == nil && time.Since(fi.ModTime()) < c.ttl {
			if data, err := os.ReadFile(cacheFile); err == nil && json.Unmarshal(data, out) == nil {
				return nil
			}
		}
	}

	if !c.bearer {
		q.Set("api_key", c.key)
	}
	u := c.Base + p + "?" + q.Encode()
	for attempt := 0; ; attempt++ {
		if err := c.wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if c.bearer {
			req.Header.Set("Authorization", "Bearer "+c.key)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("TMDB %s: %w", p, redact(err, c.key))
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("TMDB %s: %w", p, err)
		}
		switch {
		case resp.StatusCode == http.StatusOK:
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("TMDB %s: %w", p, err)
			}
			if err := os.MkdirAll(c.cache, 0o755); err == nil {
				_ = os.WriteFile(cacheFile, data, 0o644)
			}
			return nil
		case resp.StatusCode == http.StatusNotFound:
			return ErrNotFound
		case resp.StatusCode == http.StatusUnauthorized:
			return ErrAuth
		case resp.StatusCode == http.StatusTooManyRequests && attempt < 3:
			wait := 2 * time.Second
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 && s < 60 {
				wait = time.Duration(s) * time.Second
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		default:
			return fmt.Errorf("TMDB %s: %s", p, resp.Status)
		}
	}
}

// wait spaces requests at least c.gap apart.
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	next := c.last.Add(c.gap)
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	c.last = next
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Until(next)):
		return nil
	}
}

// redact keeps the API key out of logged URL errors.
func redact(err error, key string) error {
	if key == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), key, "***"))
}

// ---- response shapes ---------------------------------------------------------

type genre struct {
	Name string `json:"name"`
}

type imageInfo struct {
	FilePath    string  `json:"file_path"`
	Lang        *string `json:"iso_639_1"`
	VoteAverage float64 `json:"vote_average"`
}

type imageSet struct {
	Logos []imageInfo `json:"logos"`
}

type searchResult struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`          // movies
	OriginalTitle string `json:"original_title"` // movies
	Name          string `json:"name"`           // tv
	OriginalName  string `json:"original_name"`  // tv
	ReleaseDate   string `json:"release_date"`
	FirstAirDate  string `json:"first_air_date"`
}

type searchResponse struct {
	Results []searchResult `json:"results"`
}

type details struct {
	ID           int64    `json:"id"`
	Title        string   `json:"title"` // movies
	Name         string   `json:"name"`  // tv
	Overview     string   `json:"overview"`
	Genres       []genre  `json:"genres"`
	VoteAverage  float64  `json:"vote_average"`
	VoteCount    int      `json:"vote_count"`
	ReleaseDate  string   `json:"release_date"`
	FirstAirDate string   `json:"first_air_date"`
	PosterPath   string   `json:"poster_path"`
	BackdropPath string   `json:"backdrop_path"`
	Images       imageSet `json:"images"`
}

type seasonEpisode struct {
	ID            int64   `json:"id"`
	EpisodeNumber int     `json:"episode_number"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	StillPath     string  `json:"still_path"`
	VoteAverage   float64 `json:"vote_average"`
}

type season struct {
	Episodes []seasonEpisode `json:"episodes"`
}

func yearOf(date string) int {
	if len(date) >= 4 {
		if y, err := strconv.Atoi(date[:4]); err == nil {
			return y
		}
	}
	return 0
}
