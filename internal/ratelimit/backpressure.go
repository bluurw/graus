package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Backpressure monitora hosts que devolvem 429/503 e reduz
// temporariamente a taxa de requisições para eles.
type Backpressure struct {
	mu      sync.Mutex
	hosts   map[string]*hostState
	enabled bool
}

type hostState struct {
	failures    int
	lastFailure time.Time
	pausedUntil time.Time
}

// NewBackpressure cria um monitor. Se enabled=false, todas as chamadas
// são no-op.
func NewBackpressure(enabled bool) *Backpressure {
	return &Backpressure{
		hosts:   map[string]*hostState{},
		enabled: enabled,
	}
}

// RecordFailure registra um 429/503. Após 3 falhas seguidas,
// pausa o host por 60s.
func (b *Backpressure) RecordFailure(host string) {
	if !b.enabled {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.hosts[host]
	if !ok {
		st = &hostState{}
		b.hosts[host] = st
	}
	st.failures++
	st.lastFailure = time.Now()
	if st.failures >= 3 {
		st.pausedUntil = time.Now().Add(60 * time.Second)
		st.failures = 0
	}
}

// RecordSuccess zera o contador de falhas.
func (b *Backpressure) RecordSuccess(host string) {
	if !b.enabled {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if st, ok := b.hosts[host]; ok {
		st.failures = 0
	}
}

// Wait bloqueia até o host sair da pausa (ou ctx cancelar).
func (b *Backpressure) Wait(ctx context.Context, host string) {
	if !b.enabled {
		return
	}
	b.mu.Lock()
	st, ok := b.hosts[host]
	b.mu.Unlock()
	if !ok || st.pausedUntil.IsZero() {
		return
	}
	d := time.Until(st.pausedUntil)
	if d <= 0 {
		return
	}
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}
