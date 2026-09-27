// Package urlutil contém funções puras para normalização, escopo e filtragem de URLs.
package urlutil

import (
	"net/url"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

// ---------------------------------------------------------------------------
// Regex (compiladas uma vez; regexp.Regexp é thread-safe para Match*)
// ---------------------------------------------------------------------------

var (
	urlRegex = regexp.MustCompile(
		`https?://[a-zA-Z0-9.-]+(?:\.[a-zA-Z]{2,})+(?:/[\w\-./?%&=:#~'$,]*[\w/])?`,
	)

	invalidRegex = regexp.MustCompile(
		`(?i)(^javascript:|\.exec|constructor|function|return|;|{|}|\*SAML|\*org|\[-a-z|^\[|[0-9]+\+|\[,|,\w+=|\$|MSIE|%3E|%3C|\*|!|:[a-zA-Z]|\s|noindex|nofollow|true|false|^/@|\([^\)]|\)[^\(|\w+\(|\([^/)]+$)`,
	)

	entrypointRegex = regexp.MustCompile(`\?[^=]+=[^&\s"']+`)
)

// URLRegex expõe a regex para uso externo (crawler).
func URLRegex() *regexp.Regexp { return urlRegex }

// ---------------------------------------------------------------------------
// Cache de domínio raiz e querystrings
// ---------------------------------------------------------------------------

var (
	rootDomainCache = sync.Map{}
	qsCache         = sync.Map{}
)

// RootDomain retorna o domínio registrável (ex.: sub.example.co.uk → example.co.uk).
func RootDomain(host string) string {
	host = strings.ToLower(host)
	if host == "" {
		return ""
	}
	if v, ok := rootDomainCache.Load(host); ok {
		return v.(string)
	}
	root, _ := publicsuffix.EffectiveTLDPlusOne(host)
	if root == "" {
		root = host
	}
	rootDomainCache.Store(host, root)
	return root
}

// DomainFromURL retorna o hostname (sem porta) em lowercase.
func DomainFromURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// ---------------------------------------------------------------------------
// Normalização
// ---------------------------------------------------------------------------

// Normalize limpa e canonicaliza uma URL.
//
// Se incomplete=true e a entrada não for absoluta, retorna apenas o path
// (começando com "/").
func Normalize(raw string, incomplete bool) string {
	raw = html.UnescapeString(raw)
	trimmed := strings.Trim(raw, " .,;?!\"'<> \t\n\r()[]")
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)

	// URL incompleta (path relativo/absoluto)
	if incomplete && !strings.Contains(trimmed, "://") &&
		!strings.HasPrefix(lower, "javascript:") &&
		!strings.HasPrefix(lower, "data:") {
		if !strings.HasPrefix(trimmed, "/") {
			trimmed = "/" + trimmed
		}
		return trimmed
	}

	if invalidRegex.MatchString(trimmed) {
		return ""
	}

	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		trimmed = "https://" + trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || !strings.Contains(parsed.Host, ".") {
		return ""
	}

	// Cache de domínio raiz
	host := strings.ToLower(parsed.Hostname())
	if _, ok := rootDomainCache.Load(host); !ok {
		_ = RootDomain(host)
	}

	parsed.Fragment = ""
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.RawQuery = parsed.Query().Encode()
	return parsed.String()
}

// NormalizeKey produz uma chave para deduplicação.
func NormalizeKey(u string, incomplete bool) string {
	if incomplete {
		return strings.Trim(u, "/")
	}
	p, err := url.Parse(u)
	if err != nil || p == nil {
		return u
	}
	p.Fragment = ""
	p.RawQuery = ""
	return p.String()
}

// ---------------------------------------------------------------------------
// Escopo
// ---------------------------------------------------------------------------

// InScope verifica se a URL pertence ao conjunto de domínios permitidos.
//
// Se incomplete=true, tudo é considerado em escopo (paths relativos).
func InScope(link string, allowed map[string]struct{}, incomplete bool) bool {
	if incomplete {
		return true
	}
	u, err := url.Parse(link)
	if err != nil || u == nil {
		return false
	}
	root := RootDomain(u.Hostname())
	_, ok := allowed[root]
	return ok
}

// ---------------------------------------------------------------------------
// Filtros
// ---------------------------------------------------------------------------

var (
	// BlockedExtensions são extensões ignoradas por padrão.
	BlockedExtensions = map[string]struct{}{
		"jpg": {}, "jpeg": {}, "png": {}, "gif": {}, "bmp": {}, "svg": {}, "ico": {},
		"webp": {}, "avif": {}, "apng": {},
		"mp4": {}, "mp3": {}, "avi": {}, "mkv": {}, "mov": {}, "wmv": {}, "flv": {},
		"webm": {}, "ogg": {}, "wav": {},
		"woff": {}, "woff2": {}, "ttf": {}, "otf": {}, "eot": {},
		"css": {}, "js": {}, "map": {}, "jsonld": {},
		"pdf": {}, "zip": {}, "rar": {}, "exe": {}, "bin": {}, "iso": {}, "dmg": {}, "apk": {},
	}
)

// IsBlockedExtension verifica se a URL aponta para arquivo bloqueado.
func IsBlockedExtension(link string) bool {
	lower := strings.ToLower(link)
	// Remove query/fragment
	if i := strings.IndexAny(lower, "?#"); i != -1 {
		lower = lower[:i]
	}
	if strings.HasSuffix(lower, ".map") {
		return true
	}
	i := strings.LastIndexByte(lower, '.')
	if i == -1 || i == len(lower)-1 {
		return false
	}
	ext := lower[i+1:]
	_, blocked := BlockedExtensions[ext]
	return blocked
}

// HasValidEntrypoint verifica se a URL contém ?k=v.
func HasValidEntrypoint(link string) bool {
	u, err := url.Parse(link)
	if err != nil || u.RawQuery == "" {
		return false
	}
	if v, ok := qsCache.Load(u.RawQuery); ok {
		return v.(bool)
	}
	ok := entrypointRegex.MatchString("?" + u.RawQuery)
	qsCache.Store(u.RawQuery, ok)
	return ok
}

// Filters agrupa os parâmetros de filtragem.
type Filters struct {
	EntryPoints bool
	JSOnly      bool
	All         bool
	Incomplete  bool
	QueryList   []string
	ExcludeExt  []string
	AllowedExt  []string
	FilterStr   string
}

// Pass retorna true se o link sobrevive a todos os filtros ativos.
func Pass(link string, f Filters) bool {
	if f.All {
		return true
	}

	lowerLink := link
	if !f.JSOnly && !f.EntryPoints {
		lowerLink = strings.ToLower(link)
	}

	// Extrai extensão sem query/fragmento
	cleanExt := lowerLink
	if i := strings.IndexAny(cleanExt, "?#"); i != -1 {
		cleanExt = cleanExt[:i]
	}

	if !f.JSOnly && IsBlockedExtension(link) {
		return false
	}

	for _, e := range f.ExcludeExt {
		if strings.HasSuffix(cleanExt, "."+e) {
			return false
		}
	}

	if len(f.AllowedExt) > 0 {
		ok := false
		for _, e := range f.AllowedExt {
			if strings.HasSuffix(cleanExt, "."+e) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	if f.JSOnly && !strings.HasSuffix(cleanExt, ".js") {
		return false
	}

	if f.EntryPoints {
		return HasValidEntrypoint(link)
	}

	if f.FilterStr != "" && !strings.Contains(lowerLink, f.FilterStr) {
		return false
	}

	if len(f.QueryList) > 0 && !f.Incomplete {
		u, err := url.Parse(link)
		if err != nil || u == nil {
			return false
		}
		q := strings.ToLower(u.RawQuery)
		for _, qs := range f.QueryList {
			if strings.Contains(q, qs) {
				return true
			}
		}
		return false
	}

	return true
}
