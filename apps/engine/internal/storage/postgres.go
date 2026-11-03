// Package storage manages PostgreSQL persistence for FluxSearch documents,
// term statistics, and query analytics.
package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Document represents a full-text searchable document stored in PostgreSQL.
type Document struct {
	ID        uint64    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	URL       string    `json:"url"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Embedding stored as a comma-separated string of float32 (pgvector optional)
	EmbeddingDim int `json:"embedding_dim,omitempty"`
}

// DB wraps a pgxpool connection pool with schema management.
type DB struct {
	pool *pgxpool.Pool
}

// New creates a new DB and migrates the schema.
func New(ctx context.Context, dsn string) (*DB, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool parse config: %w", err)
	}
	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("pgxpool connect: %w", err)
	}

	db := &DB{pool: pool}
	if err := db.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("schema migration: %w", err)
	}
	return db, nil
}

// migrate creates all required tables if they do not exist.
func (db *DB) migrate(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS documents (
		id          BIGSERIAL PRIMARY KEY,
		title       TEXT      NOT NULL,
		content     TEXT      NOT NULL DEFAULT '',
		url         TEXT      NOT NULL DEFAULT '',
		tags        TEXT[]    NOT NULL DEFAULT '{}',
		created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
	);

	CREATE INDEX IF NOT EXISTS idx_documents_created_at ON documents(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_documents_tags       ON documents USING GIN(tags);

	CREATE TABLE IF NOT EXISTS term_stats (
		term        TEXT    NOT NULL,
		doc_count   INTEGER NOT NULL DEFAULT 0,
		updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (term)
	);

	CREATE TABLE IF NOT EXISTS corpus_stats (
		id              INTEGER PRIMARY KEY DEFAULT 1,
		total_docs      BIGINT  NOT NULL DEFAULT 0,
		total_tokens    BIGINT  NOT NULL DEFAULT 0,
		avg_doc_len     DOUBLE PRECISION NOT NULL DEFAULT 0,
		updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
		CHECK (id = 1)
	);

	INSERT INTO corpus_stats (id) VALUES (1) ON CONFLICT DO NOTHING;

	CREATE TABLE IF NOT EXISTS search_queries (
		id          BIGSERIAL PRIMARY KEY,
		query       TEXT         NOT NULL,
		mode        TEXT         NOT NULL DEFAULT 'bm25',
		result_count INTEGER     NOT NULL DEFAULT 0,
		latency_ms  INTEGER      NOT NULL DEFAULT 0,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
	);

	CREATE INDEX IF NOT EXISTS idx_search_queries_created_at ON search_queries(created_at DESC);
	`

	_, err := db.pool.Exec(ctx, schema)
	return err
}

// InsertDocument persists a new document and returns its assigned ID.
func (db *DB) InsertDocument(ctx context.Context, doc *Document) (uint64, error) {
	var id uint64
	err := db.pool.QueryRow(ctx,
		`INSERT INTO documents (title, content, url, tags)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		doc.Title, doc.Content, doc.URL, doc.Tags,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert document: %w", err)
	}
	return id, nil
}

// BulkInsertDocuments inserts multiple documents in a single transaction.
func (db *DB) BulkInsertDocuments(ctx context.Context, docs []*Document) ([]uint64, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	ids := make([]uint64, 0, len(docs))
	for _, doc := range docs {
		var id uint64
		err := tx.QueryRow(ctx,
			`INSERT INTO documents (title, content, url, tags)
			 VALUES ($1, $2, $3, $4)
			 RETURNING id`,
			doc.Title, doc.Content, doc.URL, doc.Tags,
		).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("bulk insert: %w", err)
		}
		ids = append(ids, id)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit bulk insert: %w", err)
	}
	return ids, nil
}

// GetDocument retrieves a document by ID.
func (db *DB) GetDocument(ctx context.Context, id uint64) (*Document, error) {
	doc := &Document{}
	err := db.pool.QueryRow(ctx,
		`SELECT id, title, content, url, tags, created_at, updated_at
		 FROM documents WHERE id = $1`,
		id,
	).Scan(&doc.ID, &doc.Title, &doc.Content, &doc.URL, &doc.Tags, &doc.CreatedAt, &doc.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get document %d: %w", id, err)
	}
	return doc, nil
}

// UpdateCorpusStats atomically updates global corpus statistics.
func (db *DB) UpdateCorpusStats(ctx context.Context, totalDocs, totalTokens int64, avgDocLen float64) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE corpus_stats
		 SET total_docs=$1, total_tokens=$2, avg_doc_len=$3, updated_at=now()
		 WHERE id=1`,
		totalDocs, totalTokens, avgDocLen,
	)
	return err
}

// GetCorpusStats retrieves current corpus statistics.
func (db *DB) GetCorpusStats(ctx context.Context) (totalDocs, totalTokens int64, avgDocLen float64, err error) {
	err = db.pool.QueryRow(ctx,
		`SELECT total_docs, total_tokens, avg_doc_len FROM corpus_stats WHERE id=1`,
	).Scan(&totalDocs, &totalTokens, &avgDocLen)
	return
}

// LogQuery records a search query for analytics.
func (db *DB) LogQuery(ctx context.Context, query, mode string, resultCount, latencyMs int) error {
	_, err := db.pool.Exec(ctx,
		`INSERT INTO search_queries (query, mode, result_count, latency_ms)
		 VALUES ($1, $2, $3, $4)`,
		query, mode, resultCount, latencyMs,
	)
	return err
}

// Health checks database connectivity.
func (db *DB) Health(ctx context.Context) error {
	return db.pool.Ping(ctx)
}

// Close closes the connection pool.
func (db *DB) Close() {
	db.pool.Close()
}
