package shard

import (
	"fmt"
	"sync"

	"github.com/davisclrk/article_db/internal/article"
	"github.com/davisclrk/article_db/internal/index"
	"github.com/davisclrk/article_db/internal/models"
	"github.com/davisclrk/article_db/internal/storage"
)

type Node struct {
	ShardID     int
	IsPrimary   bool
	store       *storage.SQLiteStore
	replicaAddr string
	mu          sync.RWMutex
}

func NewNode(shardID int, isPrimary bool, dbPath string) (*Node, error) {
	store, err := storage.NewSQLiteStore(dbPath, shardID)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	return &Node{
		ShardID:   shardID,
		IsPrimary: isPrimary,
		store:     store,
	}, nil
}

func (n *Node) SetReplicaAddr(addr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.replicaAddr = addr
}

func (n *Node) Close() error {
	return n.store.Close()
}

func (n *Node) InsertArticle(a *models.Article) error {
	if a == nil {
		return fmt.Errorf("article is nil")
	}
	return n.store.Insert(a)
}

func (n *Node) GetArticle(id string) (*models.Article, error) {
	return n.store.Get(id)
}

func (n *Node) DeleteArticle(id string) error {
	return n.store.Delete(id)
}

// SearchSimilar scores every vector on this shard with cosine similarity (brute force over SQLite rows).
func (n *Node) SearchSimilar(query []float32, k int) ([]models.SearchResult, error) {
	articles, err := n.store.GetAll()
	if err != nil {
		return nil, err
	}
	if len(articles) == 0 {
		return nil, nil
	}

	records := make([]index.Record, len(articles))
	for i, a := range articles {
		records[i] = index.Record{
			ID:     a.ID,
			Text:   article.BuildSearchText(a.Headline, a.Summary),
			Vector: a.Vector,
		}
	}

	hits, err := index.SearchRecordsBruteForce(records, query, k)
	if err != nil {
		return nil, err
	}

	byID := make(map[string]*models.Article, len(articles))
	for _, a := range articles {
		byID[a.ID] = a
	}

	out := make([]models.SearchResult, 0, len(hits))
	for _, h := range hits {
		a := byID[h.ID]
		if a == nil {
			continue
		}
		articleCopy := *a
		out = append(out, models.SearchResult{Article: articleCopy, Score: h.Score})
	}
	return out, nil
}
