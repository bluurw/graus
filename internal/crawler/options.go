package crawler

import (
	"context"
	"regexp"

	"grau/internal/ratelimit"
)

// Options controla o comportamento do crawl.
type Options struct {
	// Básico
	Retries     int
	MinBytes    int
	MaxBytes    int
	Incomplete  bool
	EntryPoints bool
	JSLoose     bool

	Limiter interface {
		Wait(ctx context.Context, host string) error
	}
	Headers map[string]string

	// Feature 2 — headless
	Headless bool

	// Feature 3 — source maps
	SourceMap bool

	// Feature 4 — deep
	Deep bool

	// Feature 7 — escopo fino
	IncludeSubdomains bool
	ExcludeHosts      []string
	ScopeRegex        string

	// Feature 9 — priorização
	Prioritize bool

	// Feature 11 — backpressure
	Backpressure bool
	BP           *ratelimit.Backpressure

	scopeRe *regexp.Regexp
}

// Compile compila regex/scope uma única vez.
func (o *Options) Compile() error {
	if o.ScopeRegex != "" && o.scopeRe == nil {
		re, err := regexp.Compile(o.ScopeRegex)
		if err != nil {
			return err
		}
		o.scopeRe = re
	}
	return nil
}

// ScopeOK checa regex de escopo, se definido.
func (o *Options) ScopeOK(rawURL string) bool {
	if o.scopeRe == nil {
		return true
	}
	return o.scopeRe.MatchString(rawURL)
}

// HostExcluded checa se o host está na lista de exclusão.
func (o *Options) HostExcluded(host string) bool {
	for _, h := range o.ExcludeHosts {
		if h == host {
			return true
		}
	}
	return false
}
