package crawler

import (
	"net/url"
	"regexp"
	"strings"

	"grau/internal/urlutil"
)

// ---------------------------------------------------------------------------
// Regex (compiladas uma vez; regexp.Regexp é thread-safe)
// ---------------------------------------------------------------------------

// cssURLRegex casa url(...) em CSS. Aceita aspas simples, duplas ou nada.
//
//	url("foo.png")
//	url('foo.png')
//	url(foo.png)
//	url( foo.png )   ← espaços internos são permitidos pela spec
var cssURLRegex = regexp.MustCompile(
	`url\(\s*(?:"([^"]*)"|'([^']*)'|([^)'"]+?))\s*\)`,
)

// cssImportRegex casa @import "..." e @import url(...).
//
//	@import "base.css";
//	@import 'base.css' screen;
//	@import url(base.css);
//	@import url("base.css") layer(base);
var cssImportRegex = regexp.MustCompile(
	`@import\s+(?:url\(\s*(?:"([^"]*)"|'([^']*)'|([^)'"]+?))\s*\)|"([^"]*)"|'([^']*)')`,
)

// ---------------------------------------------------------------------------
// API pública
// ---------------------------------------------------------------------------

// ExtractCSSLinks extrai URLs de um corpo CSS.
//
// Cobre:
//   - url(...) — imagens, fontes, SVGs, etc.
//   - @import — outros arquivos CSS
//
// Retorna URLs normalizadas e ordenadas? Não — a ordenação é responsabilidade
// do chamador. Aqui retornamos na ordem de aparição no CSS, sem duplicatas.
func ExtractCSSLinks(body []byte, baseURL *url.URL, incomplete bool) []string {
	if baseURL == nil || len(body) == 0 {
		return nil
	}
	s := string(body)

	seen := make(map[string]struct{}, 32)
	out := make([]string, 0, 32)

	// --- url(...) ---
	for _, m := range cssURLRegex.FindAllStringSubmatch(s, -1) {
		raw := firstNonEmpty(m[1], m[2], m[3])
		if raw == "" || strings.HasPrefix(raw, "data:") {
			continue
		}
		if n := resolveCSS(raw, baseURL, incomplete); n != "" {
			if _, dup := seen[n]; !dup {
				seen[n] = struct{}{}
				out = append(out, n)
			}
		}
	}

	// --- @import ... ---
	for _, m := range cssImportRegex.FindAllStringSubmatch(s, -1) {
		raw := firstNonEmpty(m[1], m[2], m[3], m[4], m[5])
		if raw == "" || strings.HasPrefix(raw, "data:") {
			continue
		}
		if n := resolveCSS(raw, baseURL, incomplete); n != "" {
			if _, dup := seen[n]; !dup {
				seen[n] = struct{}{}
				out = append(out, n)
			}
		}
	}

	return out
}

// ---------------------------------------------------------------------------
// Helpers internos
// ---------------------------------------------------------------------------

// resolveCSS resolve uma URL bruta contra a base e a normaliza.
func resolveCSS(raw string, base *url.URL, incomplete bool) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Ignora fragmentos puros (#id) — comum em SVG references
	if strings.HasPrefix(raw, "#") {
		return ""
	}

	// Protocolos que não queremos rastrear
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "data:"),
		strings.HasPrefix(lower, "blob:"),
		strings.HasPrefix(lower, "javascript:"),
		strings.HasPrefix(lower, "mailto:"),
		strings.HasPrefix(lower, "tel:"),
		strings.HasPrefix(lower, "about:"):
		return ""
	}

	rel, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	abs := base.ResolveReference(rel)
	return urlutil.Normalize(abs.String(), incomplete)
}

// firstNonEmpty devolve o primeiro argumento não-vazio.
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
