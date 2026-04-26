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
	shardMap  *models.ShardMap
	routes    map[int]*shardRoute
	mu        sync.RWMutex
	nextDocID uint64
}

type ReplicaSetConfig struct {
	PrimaryAddr string
	Primary     Client
	ReplicaAddr string
	Replica     Client
}

type Config struct {
	NumShards    int
	DataDir      string
	NewIndex     func() index.VectorIndex
	RemoteShards map[int]ReplicaSetConfig
}

func NewCoordinator(cfg Config) (*Coordinator, error) {
	if cfg.NumShards <= 0 {
		return nil, fmt.Errorf("num_shards must be positive")
	}

	shardMap := models.NewShardMap(cfg.NumShards)
	routes := make(map[int]*shardRoute, cfg.NumShards)

	if cfg.RemoteShards == nil {
		if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
			return nil, fmt.Errorf("data dir: %w", err)
		}
		for i := 0; i < cfg.NumShards; i++ {
			dbPath := fmt.Sprintf("%s/shard_%d.db", cfg.DataDir, i)
			node, err := shard.NewNode(shard.Config{
				ShardID:   i,
				IsPrimary: true,
				DBPath:    dbPath,
				NewIndex:  cfg.NewIndex,
			})
			if err != nil {
				closeAll(routes)
				return nil, fmt.Errorf("failed to create shard %d: %w", i, err)
			}
			routes[i] = newLocalShardRoute(i, NewLocalClient(node))
		}
	} else {
		if len(cfg.RemoteShards) != cfg.NumShards {
			return nil, fmt.Errorf("remote shard map has %d entries, expected %d", len(cfg.RemoteShards), cfg.NumShards)
		}
		for i := 0; i < cfg.NumShards; i++ {
			remote, ok := cfg.RemoteShards[i]
			if !ok {
				closeAll(routes)
				return nil, fmt.Errorf("missing remote shard %d", i)
			}
			if remote.Primary == nil || remote.Replica == nil {
				closeAll(routes)
				return nil, fmt.Errorf("remote shard %d requires both primary and replica clients", i)
			}
			if remote.PrimaryAddr == "" || remote.ReplicaAddr == "" {
				closeAll(routes)
				return nil, fmt.Errorf("remote shard %d requires both primary and replica addresses", i)
			}
			routes[i] = newRemoteShardRoute(i, remote)
			shardMap.Shards[i].PrimaryAddr = remote.PrimaryAddr
			shardMap.Shards[i].ReplicaAddr = remote.ReplicaAddr
		}
	}

	return &Coordinator{
		shardMap: shardMap,
		routes:   routes,
	}, nil
}

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

	route, err := c.routeFor(shardID)
	if err != nil {
		return "", err
	}
	if err := route.InsertArticle(ctx, article); err != nil {
		return "", err
	}
	return id, nil
}

func (c *Coordinator) Get(ctx context.Context, id string) (*models.Article, error) {
	if id == "" {
		return nil, fmt.Errorf("id is empty")
	}
	route, err := c.routeFor(c.shardMap.GetShardForID(id))
	if err != nil {
		return nil, err
	}
	a, err := route.GetArticle(ctx, id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, ErrNotFound
	}
	return a, nil
}

func (c *Coordinator) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is empty")
	}
	route, err := c.routeFor(c.shardMap.GetShardForID(id))
	if err != nil {
		return err
	}
	return route.DeleteArticle(ctx, id)
}

func (c *Coordinator) Query(ctx context.Context, queryVector []float32, k int) ([]models.SearchResult, error) {
	if k <= 0 {
		return nil, fmt.Errorf("k must be positive")
	}
	if len(queryVector) == 0 {
		return nil, fmt.Errorf("query vector is empty")
	}

	c.mu.RLock()
	numShards := c.shardMap.NumShards
	routes := make([]*shardRoute, numShards)
	for i := 0; i < numShards; i++ {
		routes[i] = c.routes[i]
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
			local, err := routes[shardID].SearchSimilar(ctx, queryVector, k)
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
	routes := make([]*shardRoute, numShards)
	for i := 0; i < numShards; i++ {
		routes[i] = c.routes[i]
	}
	c.mu.RUnlock()

	articles := make([]*models.Article, 0)
	for _, route := range routes {
		shardArticles, err := route.ListArticles(ctx)
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

func (c *Coordinator) routeFor(shardID int) (*shardRoute, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	route, ok := c.routes[shardID]
	if !ok {
		return nil, fmt.Errorf("unknown shard %d", shardID)
	}
	return route, nil
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
	return closeAll(c.routes)
}

func closeAll(routes map[int]*shardRoute) error {
	var firstErr error
	for _, route := range routes {
		if err := route.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
