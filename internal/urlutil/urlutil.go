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

var (
	urlRegex = regexp.MustCompile(
		`https?://[a-zA-Z0-9.-]+(?:\.[a-zA-Z]{2,})+(?:/[\w\-./?%&=:#~'$,]*[\w/])?`,
	)

	invalidRegex = regexp.MustCompile(
		`(?i)(^javascript:|\.exec|constructor|function|return|;|{|}|\*SAML|\*org|\[-a-z|^\[|[0-9]+\+|\[,|,\w+=|\$|MSIE|%3E|%3C|\*|!|:[a-zA-Z]|\s|noindex|nofollow|true|false|^/@|\([^\)]|\)[^\(|\w+\(|\([^/)]+$)`,
	)

	entrypointRegex = regexp.MustCompile(`\?[^=]+=[^&\s"']+`)
)

func URLRegex() *regexp.Regexp { return urlRegex }

var (
	rootDomainCache = sync.Map{}
	qsCache         = sync.Map{}
)

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

func DomainFromURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// Normalize limpa e canonicaliza uma URL.
func Normalize(raw string, incomplete bool) string {
	raw = html.UnescapeString(raw)
	trimmed := strings.Trim(raw, " .,;?!\"'<> \t\n\r()[]")
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)

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

	if strings.HasPrefix(trimmed, "//") {
		trimmed = "https:" + trimmed
		lower = strings.ToLower(trimmed)
	}

	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		trimmed = "https://" + trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || !strings.Contains(parsed.Host, ".") {
		return ""
	}

	host := strings.ToLower(parsed.Hostname())
	if _, ok := rootDomainCache.Load(host); !ok {
		_ = RootDomain(host)
	}

	parsed.Fragment = ""
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.RawQuery = parsed.Query().Encode()
	return parsed.String()
}

// LooksTruncated rejeita paths que parecem pedaço de URL.
//
// Casos rejeitados:
//   - path muito curto (<=4 chars) sem extensão nem query: "/a", "/co"
//   - termina em "-" ou "_" (concatenação partida)
//   - contém %CC%81 sem extensão (encoding duplo do acento agudo)
//   - termina em "/" e nada depois (só diretório)
func LooksTruncated(raw string) bool {
	if raw == "" {
		return false
	}

	// Pega só a parte path (sem scheme/host/query)
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	path := u.Path
	if path == "" {
		return false
	}

	// Último segmento depois da barra
	lastSlash := strings.LastIndex(path, "/")
	lastSegment := path
	if lastSlash >= 0 {
		lastSegment = path[lastSlash+1:]
	}

	// 1. Segmento vazio
	if lastSegment == "" {
		return false // é diretório raiz ou /foo/, aceitável
	}

	// 2. Muito curto e sem extensão/query
	if len(lastSegment) <= 4 &&
		!strings.Contains(lastSegment, ".") &&
		u.RawQuery == "" {
		return true
	}

	// 3. Termina em hífen/underscore
	lowerSeg := strings.ToLower(lastSegment)
	if strings.HasSuffix(lowerSeg, "-") || strings.HasSuffix(lowerSeg, "_") {
		return true
	}

	// 4. %CC%81 (acento agudo) partido sem extensão
	lowerPath := strings.ToLower(path)
	if strings.Contains(lowerPath, "%cc%81") && !strings.Contains(lowerSeg, ".") {
		return true
	}

	// 5. Termina com % e 2 hex chars isolado (encoding partido)
	if len(lowerSeg) > 3 && strings.HasSuffix(lowerSeg, "%") {
		return true
	}

	return false
}

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

var (
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

func IsBlockedExtension(link string) bool {
	lower := strings.ToLower(link)
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

func Pass(link string, f Filters) bool {
	if f.All {
		return true
	}

	lowerLink := link
	if !f.JSOnly && !f.EntryPoints {
		lowerLink = strings.ToLower(link)
	}

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
