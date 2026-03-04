package models

import "time"

type Article struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Headline  string    `json:"headline"`
	Summary   string    `json:"summary"`
	Content   string    `json:"content"`
	Vector    []float32 `json:"vector"`
	ShardID   int       `json:"shard_id"`
	CreatedAt time.Time `json:"created_at"`
}

type SearchResult struct {
	Article Article `json:"article"`
	Score   float64 `json:"score"`
}
