package index

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

type Record struct {
	ID     string
	Text   string
	Vector []float32
}

type SearchResult struct {
	ID    string
	Text  string
	Score float64
}

type entry struct {
	record Record
	norm   float64
	order  int
}

type LocalIndex struct {
	mu        sync.RWMutex
	dimension int
	records   []entry
}

func NewLocalIndex() *LocalIndex {
	return &LocalIndex{}
}

func (i *LocalIndex) Insert(id, text string, vector []float32) error {
	if id == "" {
		return fmt.Errorf("id cannot be empty")
	}

	norm, err := vectorNorm(vector)
	if err != nil {
		return err
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	if i.dimension == 0 {
		i.dimension = len(vector)
	} else if len(vector) != i.dimension {
		return fmt.Errorf("vector dimension mismatch: got %d want %d", len(vector), i.dimension)
	}

	i.records = append(i.records, entry{
		record: Record{
			ID:     id,
			Text:   text,
			Vector: append([]float32(nil), vector...),
		},
		norm:  norm,
		order: len(i.records),
	})

	return nil
}

func (i *LocalIndex) Search(query []float32, k int) ([]SearchResult, error) {
	if k <= 0 {
		return []SearchResult{}, nil
	}

	queryNorm, err := vectorNorm(query)
	if err != nil {
		return nil, err
	}

	i.mu.RLock()
	defer i.mu.RUnlock()

	if len(i.records) == 0 {
		return []SearchResult{}, nil
	}
	if len(query) != i.dimension {
		return nil, fmt.Errorf("query dimension mismatch: got %d want %d", len(query), i.dimension)
	}

	scored := make([]struct {
		result SearchResult
		order  int
	}, 0, len(i.records))
	for _, record := range i.records {
		score := cosineSimilarity(query, queryNorm, record.record.Vector, record.norm)
		scored = append(scored, struct {
			result SearchResult
			order  int
		}{
			result: SearchResult{
				ID:    record.record.ID,
				Text:  record.record.Text,
				Score: score,
			},
			order: record.order,
		})
	}

	sort.SliceStable(scored, func(a, b int) bool {
		if scored[a].result.Score == scored[b].result.Score {
			return scored[a].order < scored[b].order
		}
		return scored[a].result.Score > scored[b].result.Score
	})

	if k > len(scored) {
		k = len(scored)
	}

	results := make([]SearchResult, k)
	for idx := range k {
		results[idx] = scored[idx].result
	}
	return results, nil
}

func (i *LocalIndex) List() []Record {
	i.mu.RLock()
	defer i.mu.RUnlock()

	records := make([]Record, len(i.records))
	for idx, record := range i.records {
		records[idx] = Record{
			ID:     record.record.ID,
			Text:   record.record.Text,
			Vector: append([]float32(nil), record.record.Vector...),
		}
	}
	return records
}

func cosineSimilarity(a []float32, aNorm float64, b []float32, bNorm float64) float64 {
	var dot float64
	for idx := range a {
		dot += float64(a[idx] * b[idx])
	}
	return dot / (aNorm * bNorm)
}

func vectorNorm(vector []float32) (float64, error) {
	if len(vector) == 0 {
		return 0, fmt.Errorf("vector cannot be empty")
	}

	var sum float64
	for _, value := range vector {
		sum += float64(value * value)
	}

	norm := math.Sqrt(sum)
	if norm == 0 {
		return 0, fmt.Errorf("vector norm cannot be zero")
	}
	return norm, nil
}
