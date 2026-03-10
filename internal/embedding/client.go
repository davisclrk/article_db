package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

type embedRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format,omitempty"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func NewClient(apiKey, model, baseURL string, httpClient *http.Client) *Client {
	if model == "" {
		model = "text-embedding-3-small"
	}
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
		httpClient: httpClient,
	}
}

func (c *Client) Embed(ctx context.Context, input string) ([]float32, error) {
	vectors, err := c.EmbedBatch(ctx, []string{input})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

func (c *Client) EmbedBatch(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, fmt.Errorf("inputs cannot be empty")
	}
	if c.apiKey == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY is empty")
	}

	payload := embedRequest{
		Model:          c.model,
		Input:          inputs,
		EncodingFormat: "float",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var parsed embedResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, err
	}

	if resp.StatusCode >= 300 {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return nil, fmt.Errorf("embedding API error (%d): %s", resp.StatusCode, parsed.Error.Message)
		}
		return nil, fmt.Errorf("embedding API error (%d)", resp.StatusCode)
	}

	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("embedding API returned no vectors")
	}

	vectors := make([][]float32, len(inputs))
	for i := range parsed.Data {
		idx := parsed.Data[i].Index
		if idx < 0 || idx >= len(inputs) {
			return nil, fmt.Errorf("embedding API returned invalid index %d", idx)
		}
		vectors[idx] = parsed.Data[i].Embedding
	}

	for i := range vectors {
		if len(vectors[i]) == 0 {
			return nil, fmt.Errorf("missing embedding at index %d", i)
		}
	}

	return vectors, nil
}
