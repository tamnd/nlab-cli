package nlab_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/nlab-cli/nlab"
)

func newTestClient(ts *httptest.Server) *nlab.Client {
	cfg := nlab.DefaultConfig()
	cfg.BaseURL = ts.URL
	cfg.Rate = 0
	return nlab.NewClient(cfg)
}

func TestGetSendsUserAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("request carried no User-Agent")
		}
		_, _ = w.Write([]byte(`<html><body><ul></ul></body></html>`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.Search(context.Background(), "test", 5)
	if err != nil {
		t.Fatal(err)
	}
}

func TestGetRetriesOn503(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`<html><body><ul></ul></body></html>`))
	}))
	defer srv.Close()

	cfg := nlab.DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 0
	cfg.Retries = 5
	c := nlab.NewClient(cfg)

	start := time.Now()
	_, err := c.Search(context.Background(), "test", 5)
	if err != nil {
		t.Fatal(err)
	}
	if hits != 3 {
		t.Errorf("server saw %d hits, want 3", hits)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Error("retries did not back off")
	}
}

func TestSearchReturnsResults(t *testing.T) {
	body := `<html><body>
<ul>
<li>
  <a href="/nlab/show/Homotopy+type+theory">Homotopy type theory</a>
</li>
<li>
  <a href="/nlab/show/homotopy">homotopy</a>
</li>
<li>
  <a href="/nlab/show/Homotopy+group">Homotopy group</a>
</li>
</ul>
</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	results, err := c.Search(context.Background(), "homotopy", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	r := results[0]
	if r.Rank != 1 {
		t.Errorf("Rank = %d, want 1", r.Rank)
	}
	if r.Title != "Homotopy type theory" {
		t.Errorf("Title = %q, want 'Homotopy type theory'", r.Title)
	}
	if !strings.Contains(r.URL, "Homotopy") {
		t.Errorf("URL = %q, expected to contain 'Homotopy'", r.URL)
	}
}

func TestArticleExtract(t *testing.T) {
	body := `<html><body>
<div id="revision">
<h1 id="title">nLab
  category theory</h1>
<p>Category theory is a branch of mathematics that provides a highly abstract framework.</p>
<p>It deals with mathematical structures and their relationships in a way that allows for generalization across different areas of mathematics.</p>
</div>
</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	art, err := c.Article(context.Background(), "category theory")
	if err != nil {
		t.Fatal(err)
	}
	if art.Title == "" {
		t.Error("Title is empty")
	}
	if art.Summary == "" {
		t.Error("Summary is empty")
	}
	if art.URL == "" {
		t.Error("URL is empty")
	}
}

func TestArticleNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.Article(context.Background(), "NoSuchPage")
	if !errors.Is(err, nlab.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestRecentChanges(t *testing.T) {
	body := `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>nLab</title>
  <entry>
    <title type="html">Functor</title>
    <link rel="alternate" type="application/xhtml+xml" href="https://ncatlab.org/nlab/show/Functor"/>
    <updated>2024-01-15T12:30:00Z</updated>
    <author><name>WikiEditor</name></author>
  </entry>
  <entry>
    <title type="html">Monad</title>
    <link rel="alternate" type="application/xhtml+xml" href="https://ncatlab.org/nlab/show/Monad"/>
    <updated>2024-01-15T11:00:00Z</updated>
    <author><name>MathUser</name></author>
  </entry>
</feed>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	results, err := c.Recent(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "Functor" {
		t.Errorf("Title = %q, want Functor", results[0].Title)
	}
	if results[0].URL == "" {
		t.Error("URL is empty")
	}
	if results[1].Rank != 2 {
		t.Errorf("Rank = %d, want 2", results[1].Rank)
	}
}
