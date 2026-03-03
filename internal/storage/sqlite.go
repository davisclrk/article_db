package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/davisclrk/article_db/internal/models"
	_ "github.com/mattn/go-sqlite3"
)

type SQLiteStore struct {
	db      *sql.DB
	shardID int
}

func NewSQLiteStore(dbPath string, shardID int) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &SQLiteStore{
		db:      db,
		shardID: shardID,
	}

	if err := store.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS articles (
		id TEXT PRIMARY KEY,
		url TEXT NOT NULL,
		headline TEXT NOT NULL,
		summary TEXT NOT NULL,
		content TEXT NOT NULL,
		vector BLOB NOT NULL,
		shard_id INTEGER NOT NULL,
		created_at DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_articles_shard_id ON articles(shard_id);
	`
	_, err := s.db.Exec(schema)
	return err
}

func (s *SQLiteStore) Insert(article *models.Article) error {
	vectorBytes, err := json.Marshal(article.Vector)
	if err != nil {
		return fmt.Errorf("failed to serialize vector: %w", err)
	}

	query := `
	INSERT INTO articles (id, url, headline, summary, content, vector, shard_id, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = s.db.Exec(query,
		article.ID,
		article.URL,
		article.Headline,
		article.Summary,
		article.Content,
		vectorBytes,
		article.ShardID,
		article.CreatedAt,
	)
	return err
}

func (s *SQLiteStore) Get(id string) (*models.Article, error) {
	query := `SELECT id, url, headline, summary, content, vector, shard_id, created_at FROM articles WHERE id = ?`

	var article models.Article
	var vectorBytes []byte
	var createdAt string

	err := s.db.QueryRow(query, id).Scan(
		&article.ID,
		&article.URL,
		&article.Headline,
		&article.Summary,
		&article.Content,
		&vectorBytes,
		&article.ShardID,
		&createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get article: %w", err)
	}

	if err := json.Unmarshal(vectorBytes, &article.Vector); err != nil {
		return nil, fmt.Errorf("failed to deserialize vector: %w", err)
	}

	article.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &article, nil
}

func (s *SQLiteStore) Delete(id string) error {
	query := `DELETE FROM articles WHERE id = ?`
	_, err := s.db.Exec(query, id)
	return err
}

func (s *SQLiteStore) GetAll() ([]*models.Article, error) {
	query := `SELECT id, url, headline, summary, content, vector, shard_id, created_at FROM articles`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query articles: %w", err)
	}
	defer rows.Close()

	var articles []*models.Article
	for rows.Next() {
		var article models.Article
		var vectorBytes []byte
		var createdAt string

		if err := rows.Scan(
			&article.ID,
			&article.URL,
			&article.Headline,
			&article.Summary,
			&article.Content,
			&vectorBytes,
			&article.ShardID,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan article: %w", err)
		}

		if err := json.Unmarshal(vectorBytes, &article.Vector); err != nil {
			return nil, fmt.Errorf("failed to deserialize vector: %w", err)
		}

		article.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		articles = append(articles, &article)
	}

	return articles, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
