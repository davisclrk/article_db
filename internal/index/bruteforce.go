package index

import (
	"fmt"
	"sort"
	"sync"
)

type bfEntry struct {
	record Record
	norm   float64
	order  int
}

// BruteForceIndex stores vectors in memory and answers queries with a full linear scan
// and cosine similarity (O(n) per query). It implements VectorIndex.
type BruteForceIndex struct {
	mu        sync.RWMutex
	dimension int
	records   []bfEntry
}

func NewBruteForceIndex() *BruteForceIndex {
	return &BruteForceIndex{}
}

func (i *BruteForceIndex) Insert(id, text string, vector []float32) error {
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

	i.records = append(i.records, bfEntry{
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

func (i *BruteForceIndex) Delete(id string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	for idx := range i.records {
		if i.records[idx].record.ID == id {
			i.records = append(i.records[:idx], i.records[idx+1:]...)
			return nil
		}
	}
	return fmt.Errorf("id %q not found", id)
}

func (i *BruteForceIndex) Search(query []float32, k int) ([]SearchResult, error) {
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

	return searchBruteForceLocked(i.records, query, queryNorm, k), nil
}

func (i *BruteForceIndex) List() []Record {
	i.mu.RLock()
	defer i.mu.RUnlock()

	records := make([]Record, len(i.records))
	for idx, e := range i.records {
		records[idx] = Record{
			ID:     e.record.ID,
			Text:   e.record.Text,
			Vector: append([]float32(nil), e.record.Vector...),
		}
	}
	return records
}

// SearchRecordsBruteForce scores every record with cosine similarity and returns the top-k results.
func SearchRecordsBruteForce(records []Record, query []float32, k int) ([]SearchResult, error) {
	if k <= 0 {
		return []SearchResult{}, nil
	}
	queryNorm, err := vectorNorm(query)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return []SearchResult{}, nil
	}

	dim := len(records[0].Vector)
	for _, r := range records {
		if len(r.Vector) != dim {
			return nil, fmt.Errorf("vector dimension mismatch in corpus")
		}
	}
	if len(query) != dim {
		return nil, fmt.Errorf("query dimension mismatch: got %d want %d", len(query), dim)
	}

	entries := make([]bfEntry, len(records))
	for idx := range records {
		norm, err := vectorNorm(records[idx].Vector)
		if err != nil {
			return nil, err
		}
		entries[idx] = bfEntry{
			record: Record{
				ID:     records[idx].ID,
				Text:   records[idx].Text,
				Vector: records[idx].Vector,
			},
			norm:  norm,
			order: idx,
		}
	}

	return searchBruteForceLocked(entries, query, queryNorm, k), nil
}

func searchBruteForceLocked(entries []bfEntry, query []float32, queryNorm float64, k int) []SearchResult {
	scored := make([]struct {
		result SearchResult
		order  int
	}, 0, len(entries))
	for _, e := range entries {
		score := cosineSimilarity(query, queryNorm, e.record.Vector, e.norm)
		scored = append(scored, struct {
			result SearchResult
			order  int
		}{
			result: SearchResult{
				ID:    e.record.ID,
				Text:  e.record.Text,
				Score: score,
			},
			order: e.order,
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
	out := make([]SearchResult, k)
	for idx := range k {
		out[idx] = scored[idx].result
	}
	return out
}
