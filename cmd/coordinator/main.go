package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"article_db/internal/config"
	"article_db/internal/embedding"
)

func main() {
	cfg := config.Load()

	if len(os.Args) < 2 {
		log.Println("usage: go run ./cmd/coordinator \"text to embed\"")
		return
	}

	input := strings.Join(os.Args[1:], " ")
	client := embedding.NewClient(cfg.OpenRouterAPIKey, cfg.EmbeddingModel, cfg.OpenRouterBaseURL, nil)

	vector, err := client.Embed(context.Background(), input)
	if err != nil {
		log.Fatalf("embedding request failed: %v", err)
	}

	fmt.Printf("model=%s dims=%d\n", cfg.EmbeddingModel, len(vector))
	serialized, err := json.Marshal(vector)
	if err != nil {
		log.Fatalf("embedding serialization failed: %v", err)
	}
	fmt.Printf("embedding=%s\n", serialized)
}
