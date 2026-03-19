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
	hnswIndex := index.NewHNSWIndex(index.DefaultHNSWConfig())

	fmt.Printf("HNSW vector index ready (model=%s, M=%d)\n", cfg.EmbeddingModel, index.DefaultHNSWConfig().M)
	printHelp(os.Stdout)

	session := replSession{
		client:   client,
		index:    hnswIndex,
		articles: make(map[string]*models.Article),
	}
	if err := session.run(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatalf("coordinator exited with error: %v", err)
	}
}

type replSession struct {
	client   *embedding.Client
	index    index.VectorIndex
	articles map[string]*models.Article
	nextID   int
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
		case "query":
			if err := s.handleQuery(ctx, strings.TrimSpace(rest), output); err != nil {
				fmt.Fprintf(output, "error: %v\n", err)
			}
		case "list":
			s.handleList(output)
		case "help":
			printHelp(output)
		case "exit", "quit":
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

	id := s.nextDocID()
	if err := s.index.Insert(id, searchText, vector); err != nil {
		return err
	}

	s.articles[id] = &models.Article{
		ID:       id,
		URL:      url,
		Headline: headline,
		Summary:  summary,
		Content:  content,
		Vector:   vector,
	}

	fmt.Fprintf(output, "stored %s %q (dims=%d)\n", id, headline, len(vector))
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

	for idx, result := range results {
		if a, ok := s.articles[result.ID]; ok {
			fmt.Fprintf(output, "%d. score=%.6f id=%s headline=%q url=%s\n", idx+1, result.Score, result.ID, a.Headline, a.URL)
		} else {
			fmt.Fprintf(output, "%d. score=%.6f id=%s text=%q\n", idx+1, result.Score, result.ID, result.Text) // safety net
		}
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
		if a, ok := s.articles[record.ID]; ok {
			fmt.Fprintf(output, "%d. id=%s headline=%q url=%s\n", idx+1, record.ID, a.Headline, a.URL)
		} else {
			fmt.Fprintf(output, "%d. id=%s text=%q dims=%d\n", idx+1, record.ID, record.Text, len(record.Vector))
		}
	}
}

func (s *replSession) nextDocID() string {
	s.nextID++
	return fmt.Sprintf("doc-%d", s.nextID)
}

func printHelp(output io.Writer) {
	fmt.Fprintln(output, "commands:")
	fmt.Fprintln(output, "  insert <url>       fetch article, summarize, embed, and store")
	fmt.Fprintln(output, "  query <k> <text>   semantic search for top-k similar articles")
	fmt.Fprintln(output, "  list               show all stored articles")
	fmt.Fprintln(output, "  help")
	fmt.Fprintln(output, "  exit | quit")
}
