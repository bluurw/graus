package passive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type urlscanSource struct{}

func (u *urlscanSource) Name() string { return "urlscan" }

func (u *urlscanSource) Fetch(ctx context.Context, domain string) ([]string, error) {
	api := fmt.Sprintf("https://urlscan.io/api/v1/search/?q=domain:%s&size=500", domain)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "grau-recon")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 128))
		msg := strings.ReplaceAll(string(body), "\n", " ")
		msg = strings.TrimSpace(msg)
		if len(msg) > 100 {
			msg = msg[:100] + "..."
		}
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, msg)
	}

	var payload struct {
		Results []struct {
			Page struct {
				URL string `json:"url"`
			} `json:"page"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	urls := make([]string, 0, len(payload.Results))
	for _, r := range payload.Results {
		if r.Page.URL != "" {
			urls = append(urls, r.Page.URL)
		}
	}
	return dedup(urls), nil
}
