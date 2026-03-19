package article

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"
)

var sentenceSplitter = regexp.MustCompile(`([.!?])\s+`)

func Fetch(ctx context.Context, url string) (title string, content string, err error) {
	httpClient := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "article-db/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}

	article, err := readability.FromReader(resp.Body, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse article: %w", err)
	}

	title = strings.TrimSpace(article.Title)
	content = strings.TrimSpace(article.TextContent)

	if title == "" {
		return "", "", fmt.Errorf("could not extract title from %s", url)
	}
	if content == "" {
		return "", "", fmt.Errorf("could not extract content from %s", url)
	}

	return title, content, nil
}

func Summarize(content string, numSentences int) string {
	content = strings.TrimSpace(content)
	if content == "" || numSentences <= 0 {
		return ""
	}

	parts := sentenceSplitter.Split(content, -1)
	delimiters := sentenceSplitter.FindAllStringSubmatch(content, -1)

	if len(parts) <= numSentences {
		return content
	}

	var sb strings.Builder
	for i := 0; i < numSentences && i < len(parts); i++ {
		sb.WriteString(strings.TrimSpace(parts[i]))
		if i < len(delimiters) && i < numSentences-1 {
			sb.WriteString(delimiters[i][1])
			sb.WriteString(" ")
		} else if i < len(delimiters) {
			sb.WriteString(delimiters[i][1])
		}
	}

	return sb.String()
}

func BuildSearchText(headline, summary string) string {
	headline = strings.TrimSpace(headline)
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return headline
	}
	return headline + ". " + summary
}
