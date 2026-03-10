package config

import (
	"bufio"
	"log"
	"os"
	"strings"
)

type Config struct {
	OpenRouterAPIKey  string
	EmbeddingModel    string
	OpenRouterBaseURL string
}

func Load() Config {
	loadDotEnv(".env")

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		log.Fatalf("OPENROUTER_API_KEY is not set")
	}
	model := os.Getenv("OPENROUTER_EMBEDDING_MODEL")
	if model == "" {
		model = "text-embedding-3-small"
	}
	baseURL := os.Getenv("OPENROUTER_BASE_URL")
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	return Config{
		OpenRouterAPIKey:  apiKey,
		EmbeddingModel:    model,
		OpenRouterBaseURL: baseURL,
	}
}

func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}
