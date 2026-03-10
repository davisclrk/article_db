package coordinator

import (
	"fmt"
	"sync"

	"github.com/davisclrk/article_db/internal/models"
	"github.com/davisclrk/article_db/internal/shard"
)

type Coordinator struct {
	shardMap   *models.ShardMap
	shardNodes map[int]*shard.Node
	mu         sync.RWMutex
}

type Config struct {
	NumShards int
	DataDir   string
}

func NewCoordinator(cfg Config) (*Coordinator, error) {
	c := &Coordinator{
		shardMap:   models.NewShardMap(cfg.NumShards),
		shardNodes: make(map[int]*shard.Node),
	}

	// init local shard nodes, mainly for single-machine testing
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

func (c *Coordinator) Insert(article *models.Article) (string, error) {
	// TODO: implement
	return "", nil
}

func (c *Coordinator) Get(id string) (*models.Article, error) {
	// TODO: implement
	return nil, nil
}

func (c *Coordinator) Delete(id string) error {
	// TODO: implement
	return nil
}

func (c *Coordinator) Query(queryVector []float32, k int) ([]models.SearchResult, error) {
	// TODO: implement
	return nil, nil
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
