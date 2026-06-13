package nlab

// SearchResult is the record emitted for search and recent-change results.
type SearchResult struct {
	Rank  int    `json:"rank"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Article is the record emitted for fetched article introductions.
type Article struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	URL     string `json:"url"`
}
