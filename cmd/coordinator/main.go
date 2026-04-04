package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/davisclrk/article_db/internal/article"
	"github.com/davisclrk/article_db/internal/config"
	"github.com/davisclrk/article_db/internal/coordinator"
	"github.com/davisclrk/article_db/internal/embedding"
	"github.com/davisclrk/article_db/internal/index"
	"github.com/davisclrk/article_db/internal/models"
)

func main() {
	cfg := config.Load()
	if len(os.Args) > 1 {
		log.Println("usage: go run ./cmd/coordinator")
		return
	}

	client := embedding.NewClient(cfg.OpenRouterAPIKey, cfg.EmbeddingModel, cfg.OpenRouterBaseURL, nil)

	numShards := 3
	if v := os.Getenv("ARTICLE_DB_NUM_SHARDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			numShards = n
		}
	}
	dataDir := os.Getenv("ARTICLE_DB_DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	coord, err := coordinator.NewCoordinator(coordinator.Config{
		NumShards: numShards,
		DataDir:   dataDir,
		NewIndex:  newVectorIndexFromEnv,
	})
	if err != nil {
		log.Fatalf("coordinator: %v", err)
	}
	defer coord.Close()

	printIndexBanner(cfg.EmbeddingModel)
	printHelp(os.Stdout)

	session := replSession{
		client:      client,
		coordinator: coord,
	}
	if err := session.run(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatalf("coordinator exited with error: %v", err)
	}
}

func newVectorIndexFromEnv() index.VectorIndex {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ARTICLE_DB_INDEX")), "brute") {
		return index.NewBruteForceIndex()
	}
	return index.NewHNSWIndex(index.DefaultHNSWConfig())
}

func printIndexBanner(model string) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ARTICLE_DB_INDEX")), "brute") {
		fmt.Printf("Shard-local index: brute-force (model=%s)\n", model)
		fmt.Println("Query path: coordinator fanout to shard-local indexes (HNSW default, brute-force optional).")
		return
	}

	hcfg := index.DefaultHNSWConfig()
	fmt.Printf("Shard-local index: HNSW (M=%d, efConstruction=%d, efSearch=%d, model=%s)\n",
		hcfg.M, hcfg.EfConstruction, hcfg.EfSearch, model)
	fmt.Println("Query path: coordinator fanout to shard-local indexes (HNSW default, brute-force optional).")
}

type replSession struct {
	client      *embedding.Client
	coordinator *coordinator.Coordinator
}

func (s *replSession) run(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 1024), 1024*1024)

	for {
		fmt.Fprint(output, "article-db> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return err
			}
			fmt.Fprintln(output)
			return nil
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		command, rest, _ := strings.Cut(line, " ")
		switch command {
		case "insert":
			if err := s.handleInsert(ctx, strings.TrimSpace(rest), output); err != nil {
				fmt.Fprintf(output, "error: %v\n", err)
			}
		case "get":
			if err := s.handleGet(strings.TrimSpace(rest), output); err != nil {
				fmt.Fprintf(output, "error: %v\n", err)
			}
		case "delete":
			if err := s.handleDelete(strings.TrimSpace(rest), output); err != nil {
				fmt.Fprintf(output, "error: %v\n", err)
			}
		case "query":
			if err := s.handleQuery(ctx, strings.TrimSpace(rest), output); err != nil {
				fmt.Fprintf(output, "error: %v\n", err)
			}
		case "list":
			if err := s.handleList(output); err != nil {
				fmt.Fprintf(output, "error: %v\n", err)
			}
		case "help":
			printHelp(output)
		case "quit":
			return nil
		default:
			fmt.Fprintf(output, "error: unknown command %q\n", command)
			printHelp(output)
		}
	}
}

func (s *replSession) handleInsert(ctx context.Context, url string, output io.Writer) error {
	if url == "" {
		return fmt.Errorf("usage: insert <url>")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("invalid URL: must start with http:// or https://")
	}

	fmt.Fprintf(output, "fetching %s...\n", url)
	headline, content, err := article.Fetch(ctx, url)
	if err != nil {
		return fmt.Errorf("article extraction failed: %w", err)
	}

	summary := article.Summarize(content, 3)
	searchText := article.BuildSearchText(headline, summary)

	fmt.Fprintf(output, "embedding %q...\n", headline)
	vector, err := s.client.Embed(ctx, searchText)
	if err != nil {
		return fmt.Errorf("embedding request failed: %w", err)
	}

	a := &models.Article{
		URL:      url,
		Headline: headline,
		Summary:  summary,
		Content:  content,
		Vector:   vector,
	}
	id, err := s.coordinator.Insert(a)
	if err != nil {
		return err
	}

	fmt.Fprintf(output, "stored %s %q (dims=%d) shard=%d\n", id, headline, len(vector), a.ShardID)
	return nil
}

func (s *replSession) handleGet(id string, output io.Writer) error {
	if id == "" {
		return fmt.Errorf("usage: get <id>")
	}
	a, err := s.coordinator.Get(id)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "id:       %s\n", a.ID)
	fmt.Fprintf(output, "url:      %s\n", a.URL)
	fmt.Fprintf(output, "headline: %s\n", a.Headline)
	fmt.Fprintf(output, "summary:  %s\n", a.Summary)
	return nil
}

func (s *replSession) handleDelete(id string, output io.Writer) error {
	if id == "" {
		return fmt.Errorf("usage: delete <id>")
	}
	if err := s.coordinator.Delete(id); err != nil {
		return err
	}
	fmt.Fprintf(output, "deleted %s\n", id)
	return nil
}

func (s *replSession) handleQuery(ctx context.Context, args string, output io.Writer) error {
	kToken, text, ok := strings.Cut(args, " ")
	if !ok || strings.TrimSpace(kToken) == "" || strings.TrimSpace(text) == "" {
		return fmt.Errorf("usage: query <k> <text>")
	}

	k, err := strconv.Atoi(strings.TrimSpace(kToken))
	if err != nil {
		return fmt.Errorf("invalid k %q", kToken)
	}

	vector, err := s.client.Embed(ctx, strings.TrimSpace(text))
	if err != nil {
		return fmt.Errorf("embedding request failed: %w", err)
	}

	results, err := s.coordinator.Query(vector, k)
	if err != nil {
		return err
	}

	if len(results) == 0 {
		fmt.Fprintln(output, "no matches")
		return nil
	}

	for idx, r := range results {
		fmt.Fprintf(output, "%d. score=%.6f id=%s headline=%q url=%s\n",
			idx+1, r.Score, r.Article.ID, r.Article.Headline, r.Article.URL)
	}
	return nil
}

func (s *replSession) handleList(output io.Writer) error {
	articles, err := s.coordinator.ListArticles()
	if err != nil {
		return err
	}
	if len(articles) == 0 {
		fmt.Fprintln(output, "no stored articles")
		return nil
	}

	for idx, a := range articles {
		fmt.Fprintf(output, "%d. id=%s shard=%d headline=%q url=%s\n", idx+1, a.ID, a.ShardID, a.Headline, a.URL)
	}
	return nil
}

func printHelp(output io.Writer) {
	fmt.Fprintln(output, "commands:")
	fmt.Fprintln(output, "  insert <url>       fetch article, summarize, embed, and store")
	fmt.Fprintln(output, "  get <id>           show article details by ID")
	fmt.Fprintln(output, "  delete <id>        remove article by ID")
	fmt.Fprintln(output, "  query <k> <text>   semantic search for top-k similar articles")
	fmt.Fprintln(output, "  list               show stored articles across shards")
	fmt.Fprintln(output, "  help")
	fmt.Fprintln(output, "  quit")
	fmt.Fprintln(output, "env: ARTICLE_DB_INDEX=brute|hnsw  ARTICLE_DB_NUM_SHARDS  ARTICLE_DB_DATA_DIR")
}
