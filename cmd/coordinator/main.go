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
	vecIndex := newVectorIndexFromEnv()

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
	})
	if err != nil {
		log.Fatalf("coordinator: %v", err)
	}
	defer coord.Close()

	printIndexBanner(vecIndex, cfg.EmbeddingModel)
	printHelp(os.Stdout)

	session := replSession{
		client:      client,
		index:       vecIndex,
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

func printIndexBanner(vecIndex index.VectorIndex, model string) {
	switch vecIndex.(type) {
	case *index.BruteForceIndex:
		fmt.Printf("In-memory index: brute-force (model=%s)\n", model)
	case *index.HNSWIndex:
		hcfg := index.DefaultHNSWConfig()
		fmt.Printf("In-memory index: HNSW (M=%d, efConstruction=%d, efSearch=%d, model=%s)\n",
			hcfg.M, hcfg.EfConstruction, hcfg.EfSearch, model)
	default:
		fmt.Printf("In-memory index: %T (model=%s)\n", vecIndex, model)
	}
	fmt.Println("Query path: in-memory index (HNSW default, brute-force optional).")
}

type replSession struct {
	client      *embedding.Client
	index       index.VectorIndex
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
			s.handleList(output)
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
	if err := s.index.Insert(id, searchText, vector); err != nil {
		_ = s.coordinator.Delete(id)
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
	if err := s.index.Delete(id); err != nil {
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

	results, err := s.index.Search(vector, k)
	if err != nil {
		return err
	}

	if len(results) == 0 {
		fmt.Fprintln(output, "no matches")
		return nil
	}

	for idx, r := range results {
		a, err := s.coordinator.Get(r.ID)
		if err == nil && a != nil {
			fmt.Fprintf(output, "%d. score=%.6f id=%s headline=%q url=%s\n",
				idx+1, r.Score, a.ID, a.Headline, a.URL)
			continue
		}
		fmt.Fprintf(output, "%d. score=%.6f id=%s text=%q\n",
			idx+1, r.Score, r.ID, r.Text)
	}
	return nil
}

func (s *replSession) handleList(output io.Writer) {
	records := s.index.List()
	if len(records) == 0 {
		fmt.Fprintln(output, "index is empty")
		return
	}

	for idx, record := range records {
		a, err := s.coordinator.Get(record.ID)
		if err == nil && a != nil {
			fmt.Fprintf(output, "%d. id=%s shard=%d headline=%q url=%s\n", idx+1, record.ID, a.ShardID, a.Headline, a.URL)
		} else {
			fmt.Fprintf(output, "%d. id=%s text=%q dims=%d\n", idx+1, record.ID, record.Text, len(record.Vector))
		}
	}
}

func printHelp(output io.Writer) {
	fmt.Fprintln(output, "commands:")
	fmt.Fprintln(output, "  insert <url>       fetch article, summarize, embed, and store")
	fmt.Fprintln(output, "  get <id>           show article details by ID")
	fmt.Fprintln(output, "  delete <id>        remove article by ID")
	fmt.Fprintln(output, "  query <k> <text>   semantic search for top-k similar articles")
	fmt.Fprintln(output, "  list               show in-memory index entries (see env below)")
	fmt.Fprintln(output, "  help")
	fmt.Fprintln(output, "  quit")
	fmt.Fprintln(output, "env: ARTICLE_DB_INDEX=brute|hnsw  ARTICLE_DB_NUM_SHARDS  ARTICLE_DB_DATA_DIR")
}
