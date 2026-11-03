// Package api wires up the FluxSearch HTTP REST API using chi router.
// Endpoints:
//   GET  /                           → redirect to /health
//   HEAD /                           → 200 OK (Render health check)
//   GET  /health                     → {"status":"UP","docs":N,"terms":M}
//   HEAD /health                     → 200 OK
//   GET  /metrics                    → Prometheus metrics
//   POST /api/v1/index               → index a single document
//   POST /api/v1/index/bulk          → batch index (max 100 docs)
//   GET  /api/v1/search              → BM25 keyword search
//   POST /api/v1/search/vector       → ANN vector search
//   POST /api/v1/search/hybrid       → RRF hybrid search
//   GET  /api/v1/suggest             → prefix autocomplete
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/sde/fluxsearch/internal/analysis"
	"github.com/sde/fluxsearch/internal/index"
	"github.com/sde/fluxsearch/internal/query"
	"github.com/sde/fluxsearch/internal/scorer"
	"github.com/sde/fluxsearch/internal/storage"
	"github.com/sde/fluxsearch/internal/trie"
	"github.com/sde/fluxsearch/internal/vector"
)

// Handler holds all dependencies for the HTTP API.
type Handler struct {
	log     *zap.Logger
	db      *storage.DB
	idx     *index.InvertedIndex
	hnsw    *vector.HNSWIndex
	trie    *trie.PrefixTrie
	parser  *query.Parser
	scorer  *scorer.BM25Scorer
	cache   cacher // optional Redis cache (nil = disabled)
}

// cacher abstracts the Redis cache for testability.
type cacher interface {
	GetQueryCache(ctx interface{ Deadline() (time.Time, bool) }, key string, dest interface{}) (bool, error)
	SetQueryCache(ctx interface{ Deadline() (time.Time, bool) }, key string, value interface{}) error
	Health(ctx interface{ Deadline() (time.Time, bool) }) error
}

// Config holds Handler configuration.
type Config struct {
	Log    *zap.Logger
	DB     *storage.DB
	VecDim int // vector dimension (e.g. 384 for all-MiniLM-L6-v2)
}

// NewHandler creates an API handler with all sub-systems initialized.
func NewHandler(cfg Config) *Handler {
	idx := index.NewInvertedIndex()
	return &Handler{
		log:    cfg.Log,
		db:     cfg.DB,
		idx:    idx,
		hnsw:   vector.NewHNSWIndex(cfg.VecDim),
		trie:   trie.NewPrefixTrie(),
		parser: query.NewParser(analysis.AnalyzeQuery),
		scorer: scorer.NewBM25Scorer(idx),
	}
}

// Router returns the fully configured chi.Router.
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "HEAD"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: false,
		MaxAge:           300,
	}))
	r.Use(middleware.Timeout(30 * time.Second))

	// Root health routes
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/health", http.StatusMovedPermanently)
	})
	r.Head("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/health", h.handleHealth)
	r.Head("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Handle("/metrics", promhttp.Handler())

	// API v1
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/index", h.handleIndex)
		r.Post("/index/bulk", h.handleBulkIndex)
		r.Get("/search", h.handleSearch)
		r.Post("/search/vector", h.handleVectorSearch)
		r.Post("/search/hybrid", h.handleHybridSearch)
		r.Get("/suggest", h.handleSuggest)
	})

	return r
}

// ─────────────────────────────────────────────────────────────────────────────
// Health
// ─────────────────────────────────────────────────────────────────────────────

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	type healthResp struct {
		Status    string `json:"status"`
		Docs      int    `json:"docs"`
		Terms     int    `json:"terms"`
		Vectors   int    `json:"vectors"`
		TrieTerms int    `json:"trie_terms"`
		Version   string `json:"version"`
	}

	dbErr := h.db.Health(r.Context())
	status := "UP"
	if dbErr != nil {
		status = "DEGRADED"
	}

	writeJSON(w, http.StatusOK, healthResp{
		Status:    status,
		Docs:      h.idx.DocCount(),
		Terms:     h.idx.TermCount(),
		Vectors:   h.hnsw.Size(),
		TrieTerms: h.trie.Size(),
		Version:   "1.0.0",
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Indexing
// ─────────────────────────────────────────────────────────────────────────────

type indexRequest struct {
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	URL       string    `json:"url"`
	Tags      []string  `json:"tags"`
	Embedding []float32 `json:"embedding,omitempty"` // optional pre-computed vector
}

type indexResponse struct {
	DocID   uint64 `json:"doc_id"`
	Message string `json:"message"`
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	var req indexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	docID, err := h.db.InsertDocument(r.Context(), &storage.Document{
		Title: req.Title, Content: req.Content, URL: req.URL, Tags: req.Tags,
	})
	if err != nil {
		h.log.Error("insert document", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	h.indexInMemory(index.DocID(docID), req)
	writeJSON(w, http.StatusCreated, indexResponse{DocID: docID, Message: "indexed"})
}

type bulkIndexRequest struct {
	Documents []indexRequest `json:"documents"`
}

type bulkIndexResponse struct {
	DocIDs  []uint64 `json:"doc_ids"`
	Indexed int      `json:"indexed"`
}

func (h *Handler) handleBulkIndex(w http.ResponseWriter, r *http.Request) {
	var req bulkIndexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(req.Documents) == 0 {
		writeError(w, http.StatusBadRequest, "documents array is empty")
		return
	}
	if len(req.Documents) > 100 {
		writeError(w, http.StatusBadRequest, "max 100 documents per bulk request")
		return
	}

	dbDocs := make([]*storage.Document, len(req.Documents))
	for i, d := range req.Documents {
		dbDocs[i] = &storage.Document{
			Title: d.Title, Content: d.Content, URL: d.URL, Tags: d.Tags,
		}
	}

	ids, err := h.db.BulkInsertDocuments(r.Context(), dbDocs)
	if err != nil {
		h.log.Error("bulk insert", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "database error")
		return
	}

	for i, id := range ids {
		h.indexInMemory(index.DocID(id), req.Documents[i])
	}

	writeJSON(w, http.StatusCreated, bulkIndexResponse{DocIDs: ids, Indexed: len(ids)})
}

// indexInMemory adds a document to the inverted index, HNSW, and trie.
func (h *Handler) indexInMemory(docID index.DocID, req indexRequest) {
	titleTerms := analysis.AnalyzeText(req.Title)
	contentTerms := analysis.AnalyzeText(req.Content)
	tagTerms := analysis.AnalyzeText(strings.Join(req.Tags, " "))

	meta := &index.DocMeta{
		DocID: docID,
		Title: req.Title,
		URL:   req.URL,
		FieldLens: map[index.FieldType]int{
			index.FieldTitle:   len(titleTerms),
			index.FieldContent: len(contentTerms),
			index.FieldTag:     len(tagTerms),
		},
	}
	h.idx.SetDocMeta(meta)

	posns := func(n int) []uint32 {
		p := make([]uint32, n)
		for i := range p {
			p[i] = uint32(i)
		}
		return p
	}

	h.idx.IndexDocument(docID, index.FieldTitle, titleTerms, posns(len(titleTerms)))
	h.idx.IndexDocument(docID, index.FieldContent, contentTerms, posns(len(contentTerms)))
	h.idx.IndexDocument(docID, index.FieldTag, tagTerms, posns(len(tagTerms)))

	// Trie: index title words for autocomplete
	for _, term := range titleTerms {
		h.trie.Insert(term, 1)
	}

	// HNSW vector index
	if len(req.Embedding) == h.hnsw.Dim() {
		h.hnsw.Insert(vector.VectorID(docID), req.Embedding)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Search
// ─────────────────────────────────────────────────────────────────────────────

type searchResponse struct {
	Query   string              `json:"query"`
	Mode    string              `json:"mode"`
	Hits    []searchHit         `json:"hits"`
	Total   int                 `json:"total"`
	TookMs  int64               `json:"took_ms"`
}

type searchHit struct {
	DocID   uint64  `json:"doc_id"`
	Score   float64 `json:"score"`
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Snippet string  `json:"snippet,omitempty"`
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if strings.TrimSpace(q) == "" {
		writeError(w, http.StatusBadRequest, "q parameter is required")
		return
	}
	topK := 10
	if k := r.URL.Query().Get("k"); k != "" {
		if n, err := strconv.Atoi(k); err == nil && n > 0 && n <= 100 {
			topK = n
		}
	}

	start := time.Now()
	pq := h.parser.Parse(q)
	executor := query.NewExecutor(h.idx)
	results := executor.Execute(pq, topK)

	hits := make([]searchHit, len(results))
	for i, r := range results {
		hits[i] = searchHit{
			DocID: uint64(r.DocID),
			Score: r.Score,
			Title: r.Title,
			URL:   r.URL,
		}
	}

	tookMs := time.Since(start).Milliseconds()
	go h.db.LogQuery(r.Context(), q, "bm25", len(hits), int(tookMs)) //nolint:errcheck

	writeJSON(w, http.StatusOK, searchResponse{
		Query: q, Mode: "bm25", Hits: hits, Total: len(hits), TookMs: tookMs,
	})
}

type vectorSearchRequest struct {
	Embedding []float32 `json:"embedding"`
	K         int       `json:"k,omitempty"`
}

func (h *Handler) handleVectorSearch(w http.ResponseWriter, r *http.Request) {
	var req vectorSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if len(req.Embedding) == 0 {
		writeError(w, http.StatusBadRequest, "embedding is required")
		return
	}
	k := 10
	if req.K > 0 && req.K <= 100 {
		k = req.K
	}

	start := time.Now()
	results := h.hnsw.Search(req.Embedding, k)

	hits := make([]searchHit, 0, len(results))
	for _, res := range results {
		meta, ok := h.idx.GetDocMeta(index.DocID(res.ID))
		if !ok {
			continue
		}
		hits = append(hits, searchHit{
			DocID: uint64(res.ID),
			Score: float64(1.0 - res.Distance), // convert distance → similarity
			Title: meta.Title,
			URL:   meta.URL,
		})
	}

	writeJSON(w, http.StatusOK, searchResponse{
		Mode: "vector", Hits: hits, Total: len(hits), TookMs: time.Since(start).Milliseconds(),
	})
}

type hybridSearchRequest struct {
	Query     string    `json:"query"`
	Embedding []float32 `json:"embedding,omitempty"`
	K         int       `json:"k,omitempty"`
}

func (h *Handler) handleHybridSearch(w http.ResponseWriter, r *http.Request) {
	var req hybridSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}
	k := 10
	if req.K > 0 && req.K <= 100 {
		k = req.K
	}

	start := time.Now()

	// BM25 results
	pq := h.parser.Parse(req.Query)
	executor := query.NewExecutor(h.idx)
	bm25Results := executor.Execute(pq, k*3) // fetch 3× for fusion

	bm25Scored := make([]scorer.ScoredDoc, len(bm25Results))
	for i, r := range bm25Results {
		bm25Scored[i] = scorer.ScoredDoc{DocID: r.DocID, Score: r.Score}
	}

	// Vector results
	var vecResults []vector.SearchResult
	if len(req.Embedding) > 0 {
		vecResults = h.hnsw.Search(req.Embedding, k*3)
	}

	// RRF fusion
	fused := query.RRFFuse(bm25Scored, vecResults)
	if len(fused) > k {
		fused = fused[:k]
	}

	hits := make([]searchHit, 0, len(fused))
	for _, f := range fused {
		meta, ok := h.idx.GetDocMeta(f.DocID)
		if !ok {
			continue
		}
		hits = append(hits, searchHit{
			DocID: uint64(f.DocID),
			Score: f.RRFScore,
			Title: meta.Title,
			URL:   meta.URL,
		})
	}

	go h.db.LogQuery(r.Context(), req.Query, "hybrid", len(hits), int(time.Since(start).Milliseconds())) //nolint:errcheck

	writeJSON(w, http.StatusOK, searchResponse{
		Query: req.Query, Mode: "hybrid", Hits: hits, Total: len(hits),
		TookMs: time.Since(start).Milliseconds(),
	})
}

func (h *Handler) handleSuggest(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	if len(strings.TrimSpace(prefix)) < 1 {
		writeError(w, http.StatusBadRequest, "prefix parameter is required (min 1 char)")
		return
	}
	suggestions := h.trie.Complete(strings.ToLower(prefix), 8)

	type suggestResp struct {
		Prefix      string              `json:"prefix"`
		Suggestions []trie.Suggestion   `json:"suggestions"`
	}
	writeJSON(w, http.StatusOK, suggestResp{Prefix: prefix, Suggestions: suggestions})
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
