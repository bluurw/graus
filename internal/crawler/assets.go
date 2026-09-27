package crawler

import (
	"net/url"
	"strings"
)

// AssetKind identifica o tipo de recurso para escolher o extrator.
type AssetKind int

const (
	AssetNone AssetKind = iota
	AssetJS
	AssetCSS
)

// ClassifyAsset decide se um link deve ser baixado como JS ou CSS.
//
// Usa path + Content-Type já conhecido (se houver).
func ClassifyAsset(link, contentType string) AssetKind {
	lowerCT := strings.ToLower(contentType)
	if strings.Contains(lowerCT, "javascript") {
		return AssetJS
	}
	if strings.Contains(lowerCT, "text/css") {
		return AssetCSS
	}

	// Fallback: extensão do path
	u, err := url.Parse(link)
	if err != nil || u == nil {
		return AssetNone
	}
	path := strings.ToLower(u.Path)
	// Remove query já não está no Path, ok
	switch {
	case strings.HasSuffix(path, ".js"),
		strings.HasSuffix(path, ".mjs"),
		strings.HasSuffix(path, ".cjs"):
		return AssetJS
	case strings.HasSuffix(path, ".css"):
		return AssetCSS
	}
	return AssetNone
}
