// Package queue implementa uma fila com prioridade baseada em heurística.
package queue

import (
	"container/heap"
	"strings"
)

// Item é uma URL na fila com seu score.
type Item struct {
	URL      string
	Priority int
	index    int
}

// PriorityQueue é um heap ordenado por Priority decrescente.
type PriorityQueue []*Item

func (pq PriorityQueue) Len() int { return len(pq) }
func (pq PriorityQueue) Less(i, j int) bool {
	return pq[i].Priority > pq[j].Priority
}
func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}
func (pq *PriorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*Item)
	item.index = n
	*pq = append(*pq, item)
}
func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[:n-1]
	return item
}

// Score atribui prioridade a uma URL. Quanto maior, mais cedo é rastreada.
func Score(rawURL string) int {
	score := 0
	lower := strings.ToLower(rawURL)

	if strings.Contains(lower, "?") {
		score += 10
	}
	if strings.HasSuffix(lower, ".js") {
		score += 5
	}
	for _, kw := range []string{"api", "admin", "upload", "graphql", "login", "auth", "user", "account"} {
		if strings.Contains(lower, kw) {
			score += 3
		}
	}
	if strings.Contains(lower, "logout") || strings.Contains(lower, "delete") {
		score -= 5
	}
	return score
}

// New cria uma fila vazia e pronta para uso.
func New() *PriorityQueue {
	pq := &PriorityQueue{}
	heap.Init(pq)
	return pq
}

// Enqueue adiciona uma URL com seu score.
func (pq *PriorityQueue) Enqueue(u string) {
	heap.Push(pq, &Item{URL: u, Priority: Score(u)})
}

// Dequeue remove e devolve a URL de maior prioridade.
// Retorna ("", false) se a fila estiver vazia.
func (pq *PriorityQueue) Dequeue() (string, bool) {
	if pq.Len() == 0 {
		return "", false
	}
	item := heap.Pop(pq).(*Item)
	return item.URL, true
}
