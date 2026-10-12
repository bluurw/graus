package passive

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ccSource struct{}

func (c *ccSource) Name() string { return "commoncrawl" }

func (c *ccSource) Fetch(ctx context.Context, domain string) ([]string, error) {
	listURL := "https://index.commoncrawl.org/collinfo.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
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
		return nil, fmt.Errorf("collinfo status %d: %s", resp.StatusCode, msg)
	}

	var indexes []struct {
		API string `json:"cdx-api"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&indexes); err != nil {
		return nil, err
	}
	if len(indexes) == 0 {
		return nil, fmt.Errorf("no indexes available")
	}

	api := indexes[0].API
	q := fmt.Sprintf("%s?url=%s/*&output=json&limit=2000", api, domain)
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, q, nil)
	if err != nil {
		return nil, err
	}
	resp2, err := httpClient.Do(req2)
	if err != nil {
		return nil, err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp2.Body, 128))
		msg := strings.ReplaceAll(string(body), "\n", " ")
		msg = strings.TrimSpace(msg)
		if len(msg) > 100 {
			msg = msg[:100] + "..."
		}
		return nil, fmt.Errorf("cdx status %d: %s", resp2.StatusCode, msg)
	}

	var urls []string
	dec := json.NewDecoder(resp2.Body)
	for dec.More() {
		var rec struct {
			URL string `json:"url"`
		}
		if err := dec.Decode(&rec); err != nil {
			break
		}
		if rec.URL != "" {
			urls = append(urls, rec.URL)
		}
	}

	// Fallback: alguns índices devolvem texto puro (uma linha JSON por URL)
	if len(urls) == 0 {
		resp3, err := httpClient.Do(req2.Clone(ctx))
		if err == nil {
			defer resp3.Body.Close()
			sc := bufio.NewScanner(resp3.Body)
			sc.Buffer(make([]byte, 1024*1024), 1024*1024)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" {
					continue
				}
				var rec2 struct {
					URL string `json:"url"`
				}
				if err := json.Unmarshal([]byte(line), &rec2); err == nil && rec2.URL != "" {
					urls = append(urls, rec2.URL)
				}
			}
		}
	}
	return dedup(urls), nil
}
