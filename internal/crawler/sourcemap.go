package crawler

import (
	"encoding/json"
	"net/url"
	"strings"
)

// FindSourceMaps procura sourceMappingURL no JS baixado e devolve
// as URLs absolutas dos arquivos .map.
func FindSourceMaps(jsBody, jsURL string) []string {
	out := []string{}
	seen := map[string]struct{}{}

	// 1. Diretiva //# sourceMappingURL=...
	if m := extractSourceMapDirective(jsBody); m != "" {
		if n := resolveURL(m, jsURL); n != "" {
			seen[n] = struct{}{}
			out = append(out, n)
		}
	}

	// 2. Convenção: <script>.map
	if n := resolveURL(jsURL+".map", jsURL); n != "" {
		if _, dup := seen[n]; !dup {
			seen[n] = struct{}{}
			out = append(out, n)
		}
	}

	return out
}

// ParseSourceMapContent extrai URLs do campo "sources" e "sourcesContent".
func ParseSourceMapContent(data []byte, mapURL string) []string {
	var sm struct {
		Sources        []string `json:"sources"`
		SourcesContent []string `json:"sourcesContent"`
	}
	if err := json.Unmarshal(data, &sm); err != nil {
		return nil
	}

	base, err := url.Parse(mapURL)
	if err != nil {
		return nil
	}

	out := []string{}
	seen := map[string]struct{}{}

	add := func(s string) {
		if s == "" {
			return
		}
		if strings.HasPrefix(s, "webpack://") || strings.HasPrefix(s, "ng://") {
			return
		}
		u, err := url.Parse(s)
		if err != nil {
			return
		}
		abs := base.ResolveReference(u).String()
		if _, dup := seen[abs]; !dup {
			seen[abs] = struct{}{}
			out = append(out, abs)
		}
	}

	for _, s := range sm.Sources {
		add(s)
	}
	// sourcesContent tem o código-fonte ORIGINAL — passa pela extração de JS
	for _, content := range sm.SourcesContent {
		if content == "" {
			continue
		}
		base2, _ := url.Parse(mapURL)
		for _, l := range ExtractJSLinks([]byte(content), base2, false, true) {
			if _, dup := seen[l]; !dup {
				seen[l] = struct{}{}
				out = append(out, l)
			}
		}
	}
	return out
}

func extractSourceMapDirective(body string) string {
	const marker = "sourceMappingURL="
	idx := strings.LastIndex(body, marker)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(marker):]
	if i := strings.IndexAny(rest, "\r\n"); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest)
}

func resolveURL(raw, base string) string {
	bu, err := url.Parse(base)
	if err != nil {
		return ""
	}
	ru, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return bu.ResolveReference(ru).String()
}
