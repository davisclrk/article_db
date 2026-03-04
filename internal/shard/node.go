package shard

import (
	"fmt"
	"sync"

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

func (n *Node) Close() error {
	return n.store.Close()
}
