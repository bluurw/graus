// Package crawler implementa o motor HTTP + parsing de links.
package crawler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"grau/internal/urlutil"
)

// MaxBodySize é o tamanho máximo do corpo baixado.
const MaxBodySize = 20 * 1024 * 1024

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
}

var urlAttrs = map[string][]string{
	"a":      {"href"},
	"link":   {"href"},
	"script": {"src"},
	"img":    {"src", "srcset"},
	"iframe": {"src"},
	"form":   {"action"},
	"source": {"src", "srcset"},
	"object": {"data"},
	"embed":  {"src"},
	"video":  {"src"},
	"audio":  {"src"},
	"area":   {"href"},
	"base":   {"href"},
	"input":  {"src"},
	"track":  {"src"},
}

// Client é o HTTP client compartilhado (não segue redirects automaticamente).
var Client = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        500,
		MaxIdleConnsPerHost: 150,
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
		DisableCompression:  true,
		ForceAttemptHTTP2:   true,
	},
	Timeout: 30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// Result é o resultado de um crawl de uma URL.
type Result struct {
	Links  []string
	Status int
	RT     int64
	CT     string
	Err    error
}

// Options controla o comportamento do crawl de uma URL.
type Options struct {
	Retries     int
	MinBytes    int
	MaxBytes    int // -1 = ilimitado
	Incomplete  bool
	EntryPoints bool
	JSLoose     bool // extração agressiva de strings JS (camada 3)
}

// Fetch baixa uma URL e extrai links.
func Fetch(ctx context.Context, urlStr string, opt Options) Result {
	start := time.Now()
	fixedURL := urlStr
	lower := strings.ToLower(fixedURL)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		fixedURL = "https://" + fixedURL
	}

	var lastErr error
	for attempt := 0; attempt < opt.Retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return Result{Err: err}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fixedURL, nil)
		if err != nil {
			return Result{Err: err}
		}
		req.Header.Set("User-Agent", userAgents[attempt%len(userAgents)])
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")

		resp, err := Client.Do(req)
		if err != nil {
			lastErr = err
			if attempt < opt.Retries-1 {
				if !sleepCtx(ctx, time.Duration(attempt+1)*200*time.Millisecond) {
					return Result{Err: ctx.Err()}
				}
				continue
			}
			return Result{Err: lastErr}
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxBodySize))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt < opt.Retries-1 {
				if !sleepCtx(ctx, time.Duration(attempt+1)*200*time.Millisecond) {
					return Result{Err: ctx.Err()}
				}
				continue
			}
			return Result{Status: resp.StatusCode, RT: time.Since(start).Milliseconds(), Err: lastErr}
		}

		if resp.StatusCode != http.StatusOK {
			// Redirects: devolve a Location normalizada
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				if loc := resp.Header.Get("Location"); loc != "" {
					if n := urlutil.Normalize(loc, opt.Incomplete || opt.EntryPoints); n != "" {
						return Result{
							Links:  []string{n},
							Status: resp.StatusCode,
							RT:     time.Since(start).Milliseconds(),
						}
					}
				}
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
			if attempt < opt.Retries-1 {
				if !sleepCtx(ctx, time.Duration(attempt+1)*200*time.Millisecond) {
					return Result{Err: ctx.Err()}
				}
				continue
			}
			return Result{
				Status: resp.StatusCode,
				RT:     time.Since(start).Milliseconds(),
				Err:    lastErr,
			}
		}

		if len(body) < opt.MinBytes || (opt.MaxBytes > -1 && len(body) > opt.MaxBytes) {
			return Result{
				Status: resp.StatusCode,
				RT:     time.Since(start).Milliseconds(),
				Err:    fmt.Errorf("size out of range (%d bytes)", len(body)),
			}
		}

		ct := resp.Header.Get("Content-Type")
		lowerCT := strings.ToLower(ct)

		var links []string
		switch {
		case strings.Contains(lowerCT, "text/html") || lowerCT == "":
			links = extractLinks(body, ct, fixedURL, opt.Incomplete || opt.EntryPoints, opt.JSLoose)

		case strings.Contains(lowerCT, "javascript") || strings.Contains(lowerCT, "ecmascript"):
			links = ExtractJSLinks(body, mustParse(fixedURL), opt.Incomplete || opt.EntryPoints, opt.JSLoose)

		case strings.Contains(lowerCT, "text/css"):
			links = ExtractCSSLinks(body, mustParse(fixedURL), opt.Incomplete || opt.EntryPoints)
		}

		return Result{
			Links:  links,
			Status: resp.StatusCode,
			RT:     time.Since(start).Milliseconds(),
			CT:     ContentTypeLabel(ct),
		}
	}
	return Result{Err: fmt.Errorf("failed after retries: %w", lastErr)}
}

// sleepCtx dorme ou retorna false se o ctx for cancelado.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// mustParse só é usada para URLs que já sabemos válidas.
func mustParse(raw string) *url.URL {
	u, _ := url.Parse(raw)
	return u
}

// extractLinks extrai links de HTML + regex de texto bruto.
func extractLinks(body []byte, contentType, fixedURL string, incomplete, jsLoose bool) []string {
	linkSet := make(map[string]struct{}, 200)
	baseURL, _ := url.Parse(fixedURL)

	// HTML parsing (usa charset detection)
	if strings.Contains(strings.ToLower(contentType), "text/html") || contentType == "" {
		r, err := charset.NewReader(bytes.NewReader(body), contentType)
		if err == nil {
			if doc, err := html.Parse(r); err == nil {
				extractFromDOM(doc, baseURL, linkSet, incomplete, jsLoose)
			}
		}
	}

	// Fallback/reforço: regex sobre o texto bruto
	s := string(body)
	for _, m := range urlutil.URLRegex().FindAllString(s, -1) {
		if n := urlutil.Normalize(m, incomplete); n != "" {
			linkSet[n] = struct{}{}
		}
	}

	links := make([]string, 0, len(linkSet))
	for l := range linkSet {
		links = append(links, l)
	}
	sort.Strings(links)
	return links
}

// extractFromDOM percorre a árvore HTML e resolve links relativos.
func extractFromDOM(doc *html.Node, baseURL *url.URL, out map[string]struct{}, incomplete, jsLoose bool) {
	// Descobre <base href>
	base := baseURL
	var findBase func(*html.Node)
	findBase = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "base" {
			for _, a := range n.Attr {
				if a.Key == "href" {
					if b, err := url.Parse(a.Val); err == nil {
						base = baseURL.ResolveReference(b)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findBase(c)
		}
	}
	findBase(doc)

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			// <script> inline (sem src) — extrai endpoints de JS embutido
			if n.Data == "script" {
				hasSrc := false
				for _, a := range n.Attr {
					if a.Key == "src" && a.Val != "" {
						hasSrc = true
						break
					}
				}
				if !hasSrc && n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
					inline := n.FirstChild.Data
					if len(inline) > 0 {
						for _, l := range ExtractJSLinks([]byte(inline), base, incomplete, jsLoose) {
							out[l] = struct{}{}
						}
					}
				}
			}

			if attrs, ok := urlAttrs[n.Data]; ok {
				for _, attr := range attrs {
					for _, a := range n.Attr {
						if !strings.EqualFold(a.Key, attr) || a.Val == "" {
							continue
						}
						raw := strings.TrimSpace(a.Val)
						lowerRaw := strings.ToLower(raw)
						if strings.HasPrefix(lowerRaw, "javascript:") ||
							strings.HasPrefix(lowerRaw, "data:") ||
							strings.HasPrefix(raw, "#") {
							continue
						}

						// srcset tem formato "url 1x, url 2x"
						if attr == "srcset" {
							for _, part := range strings.Split(raw, ",") {
								fields := strings.Fields(strings.TrimSpace(part))
								if len(fields) > 0 {
									collectOne(fields[0], base, out, incomplete)
								}
							}
							continue
						}

						collectOne(raw, base, out, incomplete)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
}

func collectOne(raw string, base *url.URL, out map[string]struct{}, incomplete bool) {
	var resolved *url.URL
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		p, err := url.Parse(raw)
		if err != nil || p.Host == "" {
			return
		}
		resolved = p
	} else {
		ref := base
		if ref == nil {
			return
		}
		rel, err := url.Parse(raw)
		if err != nil {
			return
		}
		resolved = ref.ResolveReference(rel)
	}
	if n := urlutil.Normalize(resolved.String(), incomplete); n != "" {
		out[n] = struct{}{}
	}
}

// ContentTypeLabel classifica o Content-Type em rótulo curto.
func ContentTypeLabel(ct string) string {
	ct = strings.ToLower(ct)
	switch {
	case strings.Contains(ct, "text/html"):
		return "HTML"
	case strings.Contains(ct, "javascript"):
		return "JS"
	case strings.Contains(ct, "json"):
		return "JSON"
	case strings.Contains(ct, "css"):
		return "CSS"
	case strings.Contains(ct, "image/"):
		return "IMG"
	default:
		return "OTHER"
	}
}
