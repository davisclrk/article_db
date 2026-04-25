package coordinator

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/davisclrk/article_db/internal/index"
	"github.com/davisclrk/article_db/internal/models"
	"github.com/davisclrk/article_db/internal/shard"
)

type Coordinator struct {
	shardMap     *models.ShardMap
	shardClients map[int]Client
	mu           sync.RWMutex
	nextDocID    uint64
}

type Config struct {
	NumShards int
	DataDir   string
	NewIndex  func() index.VectorIndex
	Clients map[int]Client
}

func NewCoordinator(cfg Config) (*Coordinator, error) {
	if cfg.NumShards <= 0 {
		return nil, fmt.Errorf("num_shards must be positive")
	}

	clients := cfg.Clients
	if clients == nil {
		if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
			return nil, fmt.Errorf("data dir: %w", err)
		}
		clients = make(map[int]Client, cfg.NumShards)
		for i := 0; i < cfg.NumShards; i++ {
			dbPath := fmt.Sprintf("%s/shard_%d.db", cfg.DataDir, i)
			node, err := shard.NewNode(shard.Config{
				ShardID:   i,
				IsPrimary: true,
				DBPath:    dbPath,
				NewIndex:  cfg.NewIndex,
			})
			if err != nil {
				closeAll(clients)
				return nil, fmt.Errorf("failed to create shard %d: %w", i, err)
			}
			clients[i] = NewLocalClient(node)
		}
	} else if len(clients) != cfg.NumShards {
		return nil, fmt.Errorf("clients map has %d entries, expected %d", len(clients), cfg.NumShards)
	}

	return &Coordinator{
		shardMap:     models.NewShardMap(cfg.NumShards),
		shardClients: clients,
	}, nil
}

// Insert persists the article on the shard selected by hash(id) % num_shards.
// If article.ID is empty, a new doc ID is assigned. Returns the stored ID.
func (c *Coordinator) Insert(ctx context.Context, article *models.Article) (string, error) {
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

	client, err := c.clientFor(shardID)
	if err != nil {
		return "", err
	}
	if err := client.InsertArticle(ctx, article); err != nil {
		return "", err
	}
	return id, nil
}

// Get loads the article from the shard that owns the ID.
func (c *Coordinator) Get(ctx context.Context, id string) (*models.Article, error) {
	if id == "" {
		return nil, fmt.Errorf("id is empty")
	}
	client, err := c.clientFor(c.shardMap.GetShardForID(id))
	if err != nil {
		return nil, err
	}
	a, err := client.GetArticle(ctx, id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, ErrNotFound
	}
	return a, nil
}

// Delete removes the article from the owning shard.
func (c *Coordinator) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is empty")
	}
	client, err := c.clientFor(c.shardMap.GetShardForID(id))
	if err != nil {
		return err
	}
	existing, err := client.GetArticle(ctx, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrNotFound
	}
	return client.DeleteArticle(ctx, id)
}

// Query asks each shard for its local top-k from its shard-local vector index, then merges
// and deduplicates by article ID before returning the global top-k.
func (c *Coordinator) Query(ctx context.Context, queryVector []float32, k int) ([]models.SearchResult, error) {
	if k <= 0 {
		return nil, fmt.Errorf("k must be positive")
	}
	if len(queryVector) == 0 {
		return nil, fmt.Errorf("query vector is empty")
	}

	c.mu.RLock()
	numShards := c.shardMap.NumShards
	clients := make([]Client, numShards)
	for i := 0; i < numShards; i++ {
		clients[i] = c.shardClients[i]
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
			local, err := clients[shardID].SearchSimilar(ctx, queryVector, k)
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

func (c *Coordinator) ListArticles(ctx context.Context) ([]*models.Article, error) {
	c.mu.RLock()
	numShards := c.shardMap.NumShards
	clients := make([]Client, numShards)
	for i := 0; i < numShards; i++ {
		clients[i] = c.shardClients[i]
	}
	c.mu.RUnlock()

	articles := make([]*models.Article, 0)
	for _, client := range clients {
		shardArticles, err := client.ListArticles(ctx)
		if err != nil {
			return nil, err
		}
		articles = append(articles, shardArticles...)
	}

	sort.SliceStable(articles, func(i, j int) bool {
		if articles[i].ShardID == articles[j].ShardID {
			return articles[i].ID < articles[j].ID
		}
		return articles[i].ShardID < articles[j].ShardID
	})
	return articles, nil
}

func (c *Coordinator) clientFor(shardID int) (Client, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	client, ok := c.shardClients[shardID]
	if !ok {
		return nil, fmt.Errorf("unknown shard %d", shardID)
	}
	return client, nil
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
	return closeAll(c.shardClients)
}

func closeAll(clients map[int]Client) error {
	var firstErr error
	for _, client := range clients {
		if err := client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
