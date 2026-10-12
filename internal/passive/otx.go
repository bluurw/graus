package passive

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type otxSource struct{}

func (o *otxSource) Name() string { return "otx" }

func (o *otxSource) Fetch(ctx context.Context, domain string) ([]string, error) {
	api := fmt.Sprintf("https://otx.alienvault.com/api/v1/indicators/domain/%s/url_list?limit=500&page=1", domain)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return nil, err
	}
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
		URLList []struct {
			URL string `json:"url"`
		} `json:"url_list"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	urls := make([]string, 0, len(payload.URLList))
	for _, u := range payload.URLList {
		if u.URL != "" {
			urls = append(urls, u.URL)
		}
	}
	return dedup(urls), nil
}
