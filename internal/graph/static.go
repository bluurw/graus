package graph

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"

	"grau/internal/urlutil"
)

// GenerateStaticGraph gera um PNG via Graphviz (dot).
func GenerateStaticGraph(urls []string, outputPNG string, maxNodes int) error {
	nodeSet := make(map[string]struct{}, len(urls))
	for _, u := range urls {
		if u != "" {
			nodeSet[u] = struct{}{}
		}
	}
	nodes := make([]string, 0, len(nodeSet))
	for n := range nodeSet {
		nodes = append(nodes, n)
	}
	if len(nodes) > maxNodes {
		fmt.Fprintf(os.Stderr, "Warning: Limiting to %d nodes for static graph (from %d)\n", maxNodes, len(nodes))
		nodes = nodes[:maxNodes]
	}
	sort.Strings(nodes)

	var sb strings.Builder
	sb.WriteString("digraph G {\n")
	sb.WriteString("  rankdir=LR;\n")
	sb.WriteString("  node [shape=box, style=rounded, fontname=\"Arial\", fontsize=10];\n")
	sb.WriteString("  edge [color=\"#888888\", arrowsize=0.8];\n")

	nodeMap := make(map[string]string, len(nodes))
	for i, n := range nodes {
		u, _ := url.Parse(n)
		label := n
		if len(n) > 30 && u != nil {
			parts := strings.Split(strings.TrimRight(u.Path, "/"), "/")
			if len(parts) > 1 {
				label = "..." + parts[len(parts)-1]
			} else {
				label = domainOf(n)
			}
		}
		id := fmt.Sprintf("n%d", i)
		nodeMap[n] = id
		fmt.Fprintf(&sb, "  %s [label=\"%s\"];\n", id, escapeDot(label))
	}

	// Arestas via mesma lógica de buildEdges
	for _, e := range buildEdges(nodes) {
		fmt.Fprintf(&sb, "  %s -> %s;\n", nodeMap[e[0]], nodeMap[e[1]])
	}
	sb.WriteString("}\n")

	cmd := exec.Command("dot", "-Tpng", "-o", outputPNG)
	cmd.Stdin = strings.NewReader(sb.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to run graphviz dot: %v (output: %s)", err, string(out))
	}

	fmt.Printf("Generated static graph: %s (%d nodes)\n", outputPNG, len(nodes))
	return nil
}

// escapeDot escapa strings para labels do Graphviz.
func escapeDot(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// mantém urlutil importado para uso futuro (evita warning se removido)
var _ = urlutil.DomainFromURL
