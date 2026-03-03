package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	Coordinator CoordinatorConfig `json:"coordinator"`
	Shards      []ShardConfig     `json:"shards"`
	Embedding   EmbeddingConfig   `json:"embedding"`
}

type CoordinatorConfig struct {
	Addr      string `json:"addr"`
	NumShards int    `json:"num_shards"`
	DataDir   string `json:"data_dir"`
}

type ShardConfig struct {
	ShardID     int    `json:"shard_id"`
	PrimaryAddr string `json:"primary_addr"`
	ReplicaAddr string `json:"replica_addr"`
	DBPath      string `json:"db_path"`
}

type EmbeddingConfig struct {
	Provider   string `json:"provider"`
	APIKey     string `json:"api_key"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func DefaultConfig() *Config {
	return &Config{
		Coordinator: CoordinatorConfig{
			Addr:      ":8080",
			NumShards: 3,
			DataDir:   "./data",
		},
		Shards: []ShardConfig{
			{ShardID: 0, PrimaryAddr: ":9000", ReplicaAddr: ":9001", DBPath: "./data/shard_0.db"},
			{ShardID: 1, PrimaryAddr: ":9002", ReplicaAddr: ":9003", DBPath: "./data/shard_1.db"},
			{ShardID: 2, PrimaryAddr: ":9004", ReplicaAddr: ":9005", DBPath: "./data/shard_2.db"},
		},
		Embedding: EmbeddingConfig{
			Provider:   "openai",
			Model:      "text-embedding-3-small",
			Dimensions: 1536,
		},
	}
}
