package coordinator

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/davisclrk/article_db/internal/models"
	"github.com/davisclrk/article_db/internal/shard"
)

type Coordinator struct {
	shardMap   *models.ShardMap
	shardNodes map[int]*shard.Node
	mu         sync.RWMutex
	nextDocID  uint64
}

type Config struct {
	NumShards int
	DataDir   string
}

func NewCoordinator(cfg Config) (*Coordinator, error) {
	if cfg.NumShards <= 0 {
		return nil, fmt.Errorf("num_shards must be positive")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}

	c := &Coordinator{
		shardMap:   models.NewShardMap(cfg.NumShards),
		shardNodes: make(map[int]*shard.Node),
	}

	for i := 0; i < cfg.NumShards; i++ {
		dbPath := fmt.Sprintf("%s/shard_%d.db", cfg.DataDir, i)
		node, err := shard.NewNode(i, true, dbPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create shard %d: %w", i, err)
		}
		c.shardNodes[i] = node
	}

	return c, nil
}

// Insert persists the article on the shard selected by hash(id) % num_shards.
// If article.ID is empty, a new doc ID is assigned. Returns the stored ID.
func (c *Coordinator) Insert(article *models.Article) (string, error) {
	if article == nil {
		return "", fmt.Errorf("article is nil")
	}

	if article.ID == "" {
		n := atomic.AddUint64(&c.nextDocID, 1)
		article.ID = fmt.Sprintf("doc-%d", n)
	}
	id := article.ID

	shardID := c.shardMap.GetShardForID(id)
	article.ShardID = shardID
	if article.CreatedAt.IsZero() {
		article.CreatedAt = time.Now().UTC()
	}

	c.mu.RLock()
	node, ok := c.shardNodes[shardID]
	c.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("unknown shard %d", shardID)
	}
	if err := node.InsertArticle(article); err != nil {
		return "", err
	}
	return id, nil
}

// Get loads the article from the shard that owns the ID.
func (c *Coordinator) Get(id string) (*models.Article, error) {
	if id == "" {
		return nil, fmt.Errorf("id is empty")
	}
	shardID := c.shardMap.GetShardForID(id)

	c.mu.RLock()
	node, ok := c.shardNodes[shardID]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown shard %d", shardID)
	}
	a, err := node.GetArticle(id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, fmt.Errorf("article %q not found", id)
	}
	return a, nil
}

// Delete removes the article from the owning shard.
func (c *Coordinator) Delete(id string) error {
	if id == "" {
		return fmt.Errorf("id is empty")
	}
	shardID := c.shardMap.GetShardForID(id)

	c.mu.RLock()
	node, ok := c.shardNodes[shardID]
	c.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown shard %d", shardID)
	}
	existing, err := node.GetArticle(id)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("article %q not found", id)
	}
	return node.DeleteArticle(id)
}

// Query asks each shard for its local top-k by cosine similarity (brute force), then merges
// and deduplicates by article ID before returning the global top-k.
func (c *Coordinator) Query(queryVector []float32, k int) ([]models.SearchResult, error) {
	if k <= 0 {
		return nil, fmt.Errorf("k must be positive")
	}
	if len(queryVector) == 0 {
		return nil, fmt.Errorf("query vector is empty")
	}

	c.mu.RLock()
	numShards := c.shardMap.NumShards
	nodes := make([]*shard.Node, numShards)
	for i := 0; i < numShards; i++ {
		nodes[i] = c.shardNodes[i]
	}
	c.mu.RUnlock()

	partials := make([][]models.SearchResult, numShards)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error

	for sid := 0; sid < numShards; sid++ {
		wg.Add(1)
		go func(shardID int) {
			defer wg.Done()
			local, err := nodes[shardID].SearchSimilar(queryVector, k)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				return
			}
			partials[shardID] = local
		}(sid)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	return mergeSearchResults(partials, k), nil
}

func mergeSearchResults(partials [][]models.SearchResult, k int) []models.SearchResult {
	best := make(map[string]models.SearchResult)
	for _, batch := range partials {
		for _, r := range batch {
			prev, ok := best[r.Article.ID]
			if !ok || r.Score > prev.Score {
				best[r.Article.ID] = r
			}
		}
	}
	out := make([]models.SearchResult, 0, len(best))
	for _, s := range best {
		out = append(out, s)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Score == out[b].Score {
			return out[a].Article.ID < out[b].Article.ID
		}
		return out[a].Score > out[b].Score
	})
	if len(out) > k {
		out = out[:k]
	}
	return out
}

func (c *Coordinator) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, node := range c.shardNodes {
		if err := node.Close(); err != nil {
			return err
		}
	}
	return nil
}
