package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/davisclrk/article_db/api/server"
	"github.com/davisclrk/article_db/internal/coordinator"
)

func main() {
	numShards := flag.Int("shards", 3, "Number of shards")
	dataDir := flag.String("data-dir", "./data", "Directory for shard databases")
	addr := flag.String("addr", ":8080", "Server address")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create data directory: %v\n", err)
		os.Exit(1)
	}

	cfg := coordinator.Config{
		NumShards: *numShards,
		DataDir:   *dataDir,
	}

	coord, err := coordinator.NewCoordinator(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create coordinator: %v\n", err)
		os.Exit(1)
	}
	defer coord.Close()

	srv := server.NewServer(coord, *addr)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nShutting down...")
		coord.Close()
		os.Exit(0)
	}()

	fmt.Printf("Coordinator starting with %d shards\n", *numShards)
	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
