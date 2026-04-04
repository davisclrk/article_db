package shard

import (
	"fmt"
	"sync"

	"github.com/davisclrk/article_db/internal/article"
	"github.com/davisclrk/article_db/internal/index"
	"github.com/davisclrk/article_db/internal/models"
	"github.com/davisclrk/article_db/internal/storage"
)

type Config struct {
	ShardID   int
	IsPrimary bool
	DBPath    string
	NewIndex  func() index.VectorIndex
}

type Node struct {
	ShardID     int
	IsPrimary   bool
	store       *storage.SQLiteStore
	vectorIndex index.VectorIndex
	replicaAddr string
	mu          sync.RWMutex
}

func NewNode(cfg Config) (*Node, error) {
	store, err := storage.NewSQLiteStore(cfg.DBPath, cfg.ShardID)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	newIndex := cfg.NewIndex
	if newIndex == nil {
		newIndex = func() index.VectorIndex {
			return index.NewHNSWIndex(index.DefaultHNSWConfig())
		}
	}
	vectorIndex := newIndex()
	if vectorIndex == nil {
		_ = store.Close()
		return nil, fmt.Errorf("failed to create vector index")
	}

	node := &Node{
		ShardID:     cfg.ShardID,
		IsPrimary:   cfg.IsPrimary,
		store:       store,
		vectorIndex: vectorIndex,
	}
	if err := node.hydrateIndex(); err != nil {
		_ = store.Close()
		return nil, err
	}
	return node, nil
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
	if err := n.store.Insert(a); err != nil {
		return err
	}
	searchText := article.BuildSearchText(a.Headline, a.Summary)
	if err := n.vectorIndex.Insert(a.ID, searchText, a.Vector); err != nil {
		if rollbackErr := n.store.Delete(a.ID); rollbackErr != nil {
			return fmt.Errorf("inserted article but failed to index: %v (rollback failed: %w)", err, rollbackErr)
		}
		return fmt.Errorf("index insert: %w", err)
	}
	return nil
}

func (n *Node) GetArticle(id string) (*models.Article, error) {
	return n.store.Get(id)
}

func (n *Node) DeleteArticle(id string) error {
	article, err := n.store.Get(id)
	if err != nil {
		return err
	}
	if article == nil {
		return fmt.Errorf("article %q not found", id)
	}
	if err := n.store.Delete(id); err != nil {
		return err
	}
	if err := n.vectorIndex.Delete(id); err != nil {
		return fmt.Errorf("index delete: %w", err)
	}
	return nil
}

func (n *Node) ListArticles() ([]*models.Article, error) {
	return n.store.GetAll()
}

// Query the shard-local vector index and load article metadata for the top-k hits.
func (n *Node) SearchSimilar(query []float32, k int) ([]models.SearchResult, error) {
	hits, err := n.vectorIndex.Search(query, k)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, nil
	}

	out := make([]models.SearchResult, 0, len(hits))
	for _, h := range hits {
		a, err := n.store.Get(h.ID)
		if err != nil {
			return nil, err
		}
		if a == nil {
			continue
		}
		articleCopy := *a
		out = append(out, models.SearchResult{Article: articleCopy, Score: h.Score})
	}
	return out, nil
}

func (n *Node) hydrateIndex() error {
	articles, err := n.store.GetAll()
	if err != nil {
		return fmt.Errorf("load shard %d articles: %w", n.ShardID, err)
	}
	for _, a := range articles {
		searchText := article.BuildSearchText(a.Headline, a.Summary)
		if err := n.vectorIndex.Insert(a.ID, searchText, a.Vector); err != nil {
			return fmt.Errorf("hydrate shard %d index for %s: %w", n.ShardID, a.ID, err)
		}
	}
	return nil
}
