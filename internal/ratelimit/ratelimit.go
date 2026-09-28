// Package ratelimit fornece um limitador de taxa por host,
// com suporte a Retry-After e backoff exponencial.
package ratelimit

import (
	"context"
	"sync"

	"golang.org/x/time/rate"
)

// HostLimiter mantém um *rate.Limiter por host.
//
// Limita o número de requisições por segundo feitas a cada host
// individualmente. Hosts diferentes têm limites independentes.
type HostLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rps      rate.Limit
	burst    int
}

// New cria um limitador com N requisições por segundo e burst B por host.
func New(rps float64, burst int) *HostLimiter {
	if rps <= 0 {
		return &HostLimiter{limiters: map[string]*rate.Limiter{}} // desabilitado
	}
	return &HostLimiter{
		limiters: make(map[string]*rate.Limiter),
		rps:      rate.Limit(rps),
		burst:    burst,
	}
}

// Wait bloqueia até que uma requisição para `host` seja permitida.
// Retorna false se ctx for cancelado antes.
func (h *HostLimiter) Wait(ctx context.Context, host string) error {
	if h.rps <= 0 || host == "" {
		return nil
	}
	h.mu.Lock()
	lim, ok := h.limiters[host]
	if !ok {
		lim = rate.NewLimiter(h.rps, h.burst)
		h.limiters[host] = lim
	}
	h.mu.Unlock()
	return lim.Wait(ctx)
}
