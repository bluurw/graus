package passive

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type waybackSource struct{}

func (w *waybackSource) Name() string { return "wayback" }

func (w *waybackSource) Fetch(ctx context.Context, domain string) ([]string, error) {
	vals := url.Values{}
	vals.Set("url", domain+"/*")
	vals.Set("output", "text")
	vals.Set("fl", "original")
	vals.Set("collapse", "urlkey")
	vals.Set("limit", "5000")

	api := "https://web.archive.org/cdx/search/cdx?" + vals.Encode()

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
		msg = strings.ReplaceAll(msg, "\r", " ")
		msg = strings.TrimSpace(msg)
		if len(msg) > 100 {
			msg = msg[:100] + "..."
		}
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, msg)
	}

	var urls []string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		urls = append(urls, line)
	}
	return dedup(urls), nil
}
