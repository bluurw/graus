// Package crawler: extração de URLs de robots.txt e sitemap.xml.
package crawler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"

	"grau/internal/urlutil"
)

// fetchRobots busca /robots.txt e extrai:
//   - Disallow:/Allow: paths (viram URLs relativas ao host)
//   - linhas Sitemap: (retornam URLs de sitemap.xml)
//
// Retorna (paths, sitemaps). Erros de rede são silenciosos — se
// robots.txt não existe, devolve vazio.
func (c *fetchContext) fetchRobots() (paths []string, sitemaps []string) {
	body, ok := c.get(c.base.String() + "/robots.txt")
	if !ok {
		return nil, nil
	}
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Case-insensitive
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "disallow:"):
			p := strings.TrimSpace(line[len("disallow:"):])
			if p != "" && p != "/" {
				// Remove wildcards básicos
				p = strings.TrimSuffix(p, "*")
				if p != "" && !strings.ContainsAny(p, "*?") {
					paths = append(paths, p)
				}
			}
		case strings.HasPrefix(lower, "allow:"):
			p := strings.TrimSpace(line[len("allow:"):])
			if p != "" && p != "/" && !strings.ContainsAny(p, "*?") {
				paths = append(paths, p)
			}
		case strings.HasPrefix(lower, "sitemap:"):
			s := strings.TrimSpace(line[len("sitemap:"):])
			if s != "" {
				sitemaps = append(sitemaps, s)
			}
		}
	}
	return paths, sitemaps
}

// fetchSitemap busca um sitemap.xml (ou sitemap index) e devolve URLs.
//
// Suporta:
//   - <urlset><url><loc>...</loc></url></urlset>
//   - <sitemapindex><sitemap><loc>...</loc></sitemap></sitemapindex>  (recursivo, 1 nível)
func (c *fetchContext) fetchSitemap(sitemapURL string) []string {
	body, ok := c.get(sitemapURL)
	if !ok {
		return nil
	}

	type loc struct {
		Loc string `xml:"loc"`
	}
	type urlset struct {
		URLs []loc `xml:"url"`
	}
	type sitemapindex struct {
		Sitemaps []loc `xml:"sitemap"`
	}

	// Detecção simples de tipo
	trimmed := bytes.TrimSpace(body)
	var out []string

	if bytes.Contains(trimmed[:min(len(trimmed), 512)], []byte("<sitemapindex")) {
		var idx sitemapindex
		if err := xml.Unmarshal(body, &idx); err == nil {
			for _, s := range idx.Sitemaps {
				out = append(out, c.fetchSitemap(s.Loc)...)
			}
		}
		return out
	}

	var us urlset
	if err := xml.Unmarshal(body, &us); err == nil {
		for _, u := range us.URLs {
			if n := urlutil.Normalize(u.Loc, c.incomplete); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

// FetchWellKnown baixa robots.txt + sitemaps e devolve URLs úteis.
//
// É o ponto de entrada usado pelo crawler depois de resolver a URL base.
func FetchWellKnown(ctx context.Context, host string, incomplete, jsLoose bool) []string {
	base, err := url.Parse("https://" + host)
	if err != nil {
		return nil
	}

	fc := &fetchContext{
		ctx:        ctx,
		base:       base,
		incomplete: incomplete,
		jsLoose:    jsLoose,
		seen:       map[string]struct{}{},
	}
	fc.client = Client

	var out []string

	// robots.txt
	paths, sitemaps := fc.fetchRobots()
	for _, p := range paths {
		if n := urlutil.Normalize(host+"/"+strings.TrimPrefix(p, "/"), incomplete); n != "" {
			if _, dup := fc.seen[n]; !dup {
				fc.seen[n] = struct{}{}
				out = append(out, n)
			}
		}
	}

	// sitemaps declarados + default /sitemap.xml
	if len(sitemaps) == 0 {
		sitemaps = append(sitemaps, base.String()+"/sitemap.xml")
	}
	for _, sm := range sitemaps {
		for _, u := range fc.fetchSitemap(sm) {
			if _, dup := fc.seen[u]; !dup {
				fc.seen[u] = struct{}{}
				out = append(out, u)
			}
		}
	}

	return out
}

// fetchContext encapsula estado temporário de uma coleta de well-known.
type fetchContext struct {
	ctx        context.Context
	client     *http.Client
	base       *url.URL
	incomplete bool
	jsLoose    bool
	seen       map[string]struct{}
}

// get faz um GET simples com ctx e devolve corpo ou nil.
func (c *fetchContext) get(url string) ([]byte, bool) {
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("User-Agent", userAgents[0])
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, false
	}
	return b, true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
