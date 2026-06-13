// Package nlab is the library behind the nlab command: the HTTP client,
// request shaping, and the typed data models for the nLab mathematics wiki.
//
// The nLab runs on Instiki and does not expose a JSON API. This package
// scrapes the public HTML pages using only the Go standard library:
//   - Search results:  GET https://ncatlab.org/nlab/search?query=QUERY
//   - Article intro:   GET https://ncatlab.org/nlab/show/TITLE
//   - Recent changes:  GET https://ncatlab.org/nlab/atom_with_headlines  (Atom XML)
package nlab

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DefaultUserAgent identifies the client to the nLab.
const DefaultUserAgent = "nlab/dev (+https://github.com/tamnd/nlab-cli)"

// ErrNotFound is returned when a page does not exist on nLab.
var ErrNotFound = errors.New("not found")

// Config holds constructor parameters for Client.
type Config struct {
	BaseURL   string
	UserAgent string
	Rate      time.Duration
	Retries   int
	Timeout   time.Duration
}

// DefaultConfig returns sensible defaults for talking to ncatlab.org.
func DefaultConfig() Config {
	return Config{
		BaseURL:   "https://ncatlab.org",
		UserAgent: DefaultUserAgent,
		Rate:      200 * time.Millisecond,
		Retries:   3,
		Timeout:   30 * time.Second,
	}
}

// Client talks to the nLab over HTTP.
type Client struct {
	httpClient *http.Client
	userAgent  string
	baseURL    string
	rate       time.Duration
	retries    int
	mu         sync.Mutex
	last       time.Time
}

// NewClient returns a Client configured with cfg.
func NewClient(cfg Config) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: cfg.Timeout},
		userAgent:  cfg.UserAgent,
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		rate:       cfg.Rate,
		retries:    cfg.Retries,
	}
}

// ─── HTTP layer ───────────────────────────────────────────────────────────────

// get fetches rawURL with pacing and retries.
func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.do(ctx, rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", rawURL, lastErr)
}

func (c *Client) do(ctx context.Context, rawURL string) ([]byte, bool, error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, true, err
	}
	return b, false, nil
}

func (c *Client) pace() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rate <= 0 {
		return
	}
	if wait := c.rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 500 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// ─── regex patterns ───────────────────────────────────────────────────────────

var (
	// <li>\n  <a href="/nlab/show/...">Title</a>
	reSearchLI = regexp.MustCompile(`(?s)<li>\s*<a href="(/nlab/show/[^"]+)">([^<]+)</a>`)
	// <h1...>...</h1> — full element; we strip tags from the text
	reH1    = regexp.MustCompile(`(?s)<h1[^>]*>(.*?)</h1>`)
	rePara  = regexp.MustCompile(`<p>[^<]{30,}`)
	reTag   = regexp.MustCompile(`<[^>]+>`)
	reSpace = regexp.MustCompile(`\s+`)
)

// ─── API methods ─────────────────────────────────────────────────────────────

// Search searches nLab pages for query and returns up to limit results.
// It calls /nlab/search?query=QUERY and parses the HTML result list.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}
	u := c.baseURL + "/nlab/search?query=" + url.QueryEscape(query)
	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}

	matches := reSearchLI.FindAllSubmatch(body, -1)
	out := make([]SearchResult, 0, len(matches))
	for i, m := range matches {
		if i >= limit {
			break
		}
		path := string(m[1])
		title := htmlDecode(string(m[2]))
		out = append(out, SearchResult{
			Rank:  i + 1,
			Title: title,
			URL:   c.baseURL + path,
		})
	}
	return out, nil
}

// Article fetches the intro of the named nLab page.
// Returns ErrNotFound when the page does not exist.
func (c *Client) Article(ctx context.Context, title string) (Article, error) {
	slug := strings.ReplaceAll(title, " ", "+")
	rawURL := c.baseURL + "/nlab/show/" + slug
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return Article{}, err
	}

	html := string(body)

	// Extract page title from the h1 that contains "nLab". The h1 includes
	// inline SVG and span tags, so we strip all tags before extracting the text.
	pageTitle := title
	for _, m := range reH1.FindAllStringSubmatch(html, -1) {
		text := reTag.ReplaceAllString(m[1], " ")
		text = reSpace.ReplaceAllString(text, " ")
		text = strings.TrimSpace(text)
		// The outer h1 reads "nLab <page title>"
		if strings.HasPrefix(text, "nLab ") {
			pageTitle = strings.TrimPrefix(text, "nLab ")
			break
		}
	}

	// The nLab layout places a sidebar (class="rightHandSide") before the
	// main article body. Skip past it to avoid extracting sidebar text.
	mainContent := html
	rhsIdx := strings.Index(html, `class="rightHandSide"`)
	if rhsIdx >= 0 {
		// Walk divs to find the closing tag of the rightHandSide div.
		after := html[rhsIdx:]
		depth := 0
		i := 0
		for i < len(after) {
			switch {
			case strings.HasPrefix(after[i:], "<div"):
				depth++
				i += 4
			case strings.HasPrefix(after[i:], "</div>"):
				depth--
				if depth == 0 {
					mainContent = html[rhsIdx+i+6:]
					i = len(after) // break
				} else {
					i += 6
				}
			default:
				i++
			}
		}
	}

	// Extract summary: first two meaningful paragraph texts from main content.
	var summaryParts []string
	paraMatches := rePara.FindAllString(mainContent, 15)
	for _, pm := range paraMatches {
		text := reTag.ReplaceAllString(pm, " ")
		text = htmlDecode(reSpace.ReplaceAllString(text, " "))
		text = strings.TrimSpace(text)
		if len(text) < 30 {
			continue
		}
		summaryParts = append(summaryParts, text)
		if len(summaryParts) >= 2 {
			break
		}
	}
	summary := strings.Join(summaryParts, " ")

	return Article{
		Title:   pageTitle,
		Summary: summary,
		URL:     c.baseURL + "/nlab/show/" + strings.ReplaceAll(pageTitle, " ", "+"),
	}, nil
}

// Recent returns up to limit recently changed nLab pages.
// It parses the Atom feed at /nlab/atom_with_headlines.
func (c *Client) Recent(ctx context.Context, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}
	rawURL := c.baseURL + "/nlab/atom_with_headlines"
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	var feed atomFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("parse atom feed: %w", err)
	}

	n := len(feed.Entries)
	if n > limit {
		n = limit
	}
	out := make([]SearchResult, 0, n)
	for i := 0; i < n; i++ {
		e := feed.Entries[i]
		title := htmlDecode(e.Title)
		link := ""
		for _, l := range e.Links {
			if l.Rel == "alternate" || link == "" {
				link = l.Href
			}
		}
		out = append(out, SearchResult{
			Rank:  i + 1,
			Title: title,
			URL:   link,
		})
	}
	return out, nil
}

// ─── Atom XML types ───────────────────────────────────────────────────────────

type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title   string     `xml:"title"`
	Links   []atomLink `xml:"link"`
	Updated string     `xml:"updated"`
	Author  struct {
		Name string `xml:"name"`
	} `xml:"author"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// htmlDecode decodes common HTML entities.
func htmlDecode(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&apos;", "'")
	s = strings.ReplaceAll(s, "&#233;", "é")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	return s
}
