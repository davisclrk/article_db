package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/davisclrk/article_db/internal/index"
	"github.com/davisclrk/article_db/internal/shard"
)

func main() {
	shardID := flag.Int("shard-id", 0, "Shard ID")
	isPrimary := flag.Bool("primary", true, "Is this the primary node")
	dbPath := flag.String("db-path", "", "Path to SQLite database")
	addr := flag.String("addr", ":9000", "gRPC server address")
	replicaAddr := flag.String("replica-addr", "", "Address of replica node")
	flag.Parse()

	if *dbPath == "" {
		*dbPath = fmt.Sprintf("./data/shard_%d.db", *shardID)
	}

	node, err := shard.NewNode(shard.Config{
		ShardID:   *shardID,
		IsPrimary: *isPrimary,
		DBPath:    *dbPath,
		NewIndex:  newVectorIndexFromEnv,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create shard node: %v\n", err)
		os.Exit(1)
	}
	defer node.Close()

	if *replicaAddr != "" {
		node.SetReplicaAddr(*replicaAddr)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Printf("Shard %d starting on %s (primary=%v)\n", *shardID, *addr, *isPrimary)

	// TODO: Start gRPC server for distributed deployment
	// 		 For now, this is a placeholder for when we separate shard nodes
	<-sigChan
	fmt.Println("\nShutting down shard node...")
}

func newVectorIndexFromEnv() index.VectorIndex {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ARTICLE_DB_INDEX")), "brute") {
		return index.NewBruteForceIndex()
	}
	return index.NewHNSWIndex(index.DefaultHNSWConfig())
}
