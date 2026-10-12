// Package passive coleta URLs de fontes externas (Wayback, Common Crawl,
// OTX, URLScan) SEM tocar no alvo.
package passive

import (
	"context"
	"net/http"
	"time"
)

// Source é uma fonte passiva.
type Source interface {
	Name() string
	Fetch(ctx context.Context, domain string) ([]string, error)
}

// Options controla quais fontes estão ativas.
type Options struct {
	Wayback bool
	CC      bool
	OTX     bool
	URLScan bool
	Timeout time.Duration
}

// All devolve todas as fontes ativas conforme Options.
func All(opt Options) []Source {
	var out []Source
	if opt.Wayback {
		out = append(out, &waybackSource{})
	}
	if opt.CC {
		out = append(out, &ccSource{})
	}
	if opt.OTX {
		out = append(out, &otxSource{})
	}
	if opt.URLScan {
		out = append(out, &urlscanSource{})
	}
	return out
}

// httpClient é compartilhado por todas as fontes.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// dedup ajuda a não repetir URLs entre fontes.
func dedup(urls []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		if u == "" {
			continue
		}
		if _, dup := seen[u]; !dup {
			seen[u] = struct{}{}
			out = append(out, u)
		}
	}
	return out
}
