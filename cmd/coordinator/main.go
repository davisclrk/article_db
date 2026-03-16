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

	"github.com/davisclrk/article_db/internal/config"
	"github.com/davisclrk/article_db/internal/embedding"
	"github.com/davisclrk/article_db/internal/index"
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
		client: client,
		index:  hnswIndex,
	}
	if err := session.run(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatalf("coordinator exited with error: %v", err)
	}
}

type replSession struct {
	client *embedding.Client
	index  index.VectorIndex
	nextID int
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

func (s *replSession) handleInsert(ctx context.Context, text string, output io.Writer) error {
	if text == "" {
		return fmt.Errorf("usage: insert <text>")
	}

	vector, err := s.client.Embed(ctx, text)
	if err != nil {
		return fmt.Errorf("embedding request failed: %w", err)
	}

	id := s.nextDocID()
	if err := s.index.Insert(id, text, vector); err != nil {
		return err
	}

	fmt.Fprintf(output, "stored %s dims=%d\n", id, len(vector))
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
		fmt.Fprintf(output, "%d. score=%.6f id=%s text=%q\n", idx+1, result.Score, result.ID, result.Text)
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
		fmt.Fprintf(output, "%d. id=%s text=%q dims=%d\n", idx+1, record.ID, record.Text, len(record.Vector))
	}
}

func (s *replSession) nextDocID() string {
	s.nextID++
	return fmt.Sprintf("doc-%d", s.nextID)
}

func printHelp(output io.Writer) {
	fmt.Fprintln(output, "commands:")
	fmt.Fprintln(output, "  insert <text>")
	fmt.Fprintln(output, "  query <k> <text>")
	fmt.Fprintln(output, "  list")
	fmt.Fprintln(output, "  help")
	fmt.Fprintln(output, "  exit | quit")
	fmt.Fprintln(output, "vectors are stored in memory only for the current process")
}
