package main

import (
	"bufio"
	"context"
	"flag"
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
	shardAddrs := flag.String("shard-addrs", "", "Comma-separated gRPC addresses of shard processes (e.g. :9000,:9001,:9002). When set, the coordinator runs in remote mode; when empty, shards run in-process.")
	flag.Parse()
	if flag.NArg() > 0 {
		log.Println("usage: go run ./cmd/coordinator [--shard-addrs :9000,:9001,:9002]")
		return
	}

	cfg := config.Load()
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

	coordCfg := coordinator.Config{
		NumShards: numShards,
		DataDir:   dataDir,
		NewIndex:  newVectorIndexFromEnv,
	}

	addrs := parseShardAddrs(*shardAddrs)
	if len(addrs) > 0 {
		if len(addrs) != numShards {
			log.Fatalf("--shard-addrs has %d entries but ARTICLE_DB_NUM_SHARDS=%d", len(addrs), numShards)
		}
		clients, err := dialShards(addrs)
		if err != nil {
			log.Fatalf("dial shards: %v", err)
		}
		coordCfg.Clients = clients
	}

	coord, err := coordinator.NewCoordinator(coordCfg)
	if err != nil {
		log.Fatalf("coordinator: %v", err)
	}
	defer coord.Close()

	printIndexBanner(cfg.EmbeddingModel, addrs)
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

func parseShardAddrs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func dialShards(addrs []string) (map[int]coordinator.Client, error) {
	clients := make(map[int]coordinator.Client, len(addrs))
	for i, addr := range addrs {
		c, err := coordinator.NewRemoteClient(addr)
		if err != nil {
			for _, prev := range clients {
				_ = prev.Close()
			}
			return nil, fmt.Errorf("shard %d at %s: %w", i, addr, err)
		}
		clients[i] = c
	}
	return clients, nil
}

func printIndexBanner(model string, shardAddrs []string) {
	mode := "in-process"
	if len(shardAddrs) > 0 {
		mode = fmt.Sprintf("remote shards %v", shardAddrs)
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ARTICLE_DB_INDEX")), "brute") {
		fmt.Printf("Shard-local index: brute-force (model=%s, mode=%s)\n", model, mode)
		fmt.Println("Query path: coordinator fanout to shard-local indexes (HNSW default, brute-force optional).")
		return
	}

	hcfg := index.DefaultHNSWConfig()
	fmt.Printf("Shard-local index: HNSW (M=%d, efConstruction=%d, efSearch=%d, model=%s, mode=%s)\n",
		hcfg.M, hcfg.EfConstruction, hcfg.EfSearch, model, mode)
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
			if err := s.handleGet(ctx, strings.TrimSpace(rest), output); err != nil {
				fmt.Fprintf(output, "error: %v\n", err)
			}
		case "delete":
			if err := s.handleDelete(ctx, strings.TrimSpace(rest), output); err != nil {
				fmt.Fprintf(output, "error: %v\n", err)
			}
		case "query":
			if err := s.handleQuery(ctx, strings.TrimSpace(rest), output); err != nil {
				fmt.Fprintf(output, "error: %v\n", err)
			}
		case "list":
			if err := s.handleList(ctx, output); err != nil {
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
	id, err := s.coordinator.Insert(ctx, a)
	if err != nil {
		return err
	}

	fmt.Fprintf(output, "stored %s %q (dims=%d) shard=%d\n", id, headline, len(vector), a.ShardID)
	return nil
}

func (s *replSession) handleGet(ctx context.Context, id string, output io.Writer) error {
	if id == "" {
		return fmt.Errorf("usage: get <id>")
	}
	a, err := s.coordinator.Get(ctx, id)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "id:       %s\n", a.ID)
	fmt.Fprintf(output, "url:      %s\n", a.URL)
	fmt.Fprintf(output, "headline: %s\n", a.Headline)
	fmt.Fprintf(output, "summary:  %s\n", a.Summary)
	return nil
}

func (s *replSession) handleDelete(ctx context.Context, id string, output io.Writer) error {
	if id == "" {
		return fmt.Errorf("usage: delete <id>")
	}
	if err := s.coordinator.Delete(ctx, id); err != nil {
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

	results, err := s.coordinator.Query(ctx, vector, k)
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

func (s *replSession) handleList(ctx context.Context, output io.Writer) error {
	articles, err := s.coordinator.ListArticles(ctx)
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
	fmt.Fprintln(output, "flags: --shard-addrs :9000,:9001,:9002 (remote mode; default in-process)")
	fmt.Fprintln(output, "env:   ARTICLE_DB_INDEX=brute|hnsw  ARTICLE_DB_NUM_SHARDS  ARTICLE_DB_DATA_DIR")
}
