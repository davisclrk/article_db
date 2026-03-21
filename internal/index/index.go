package index

import (
	"fmt"
	"math"
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

type VectorIndex interface {
	Insert(id, text string, vector []float32) error
	Delete(id string) error
	Search(query []float32, k int) ([]SearchResult, error)
	List() []Record
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
