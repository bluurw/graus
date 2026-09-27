// Package graph gera visualizações de grafo a partir de URLs coletadas.
package graph

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"grau/internal/urlutil"
)

// domainOf retorna o domínio registrável de uma URL como string.
func domainOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return "unknown"
	}
	return urlutil.DomainFromURL(u)
}

// buildEdges cria arestas (n1 → n2) quando n2 é descendente de n1 no mesmo domínio.
func buildEdges(nodes []string) [][2]string {
	byDomain := make(map[string][]string)
	for _, n := range nodes {
		byDomain[domainOf(n)] = append(byDomain[domainOf(n)], n)
	}

	var edges [][2]string
	for _, list := range byDomain {
		sort.Strings(list)
		for i, n1 := range list {
			for _, n2 := range list[i+1:] {
				if n2 == n1 {
					continue
				}
				if !strings.HasPrefix(n2, n1) || len(n2) <= len(n1) {
					continue
				}
				if len(n1) == len("https://")+len(domainOf(n1)) || n2[len(n1)] == '/' {
					edges = append(edges, [2]string{n1, n2})
				}
			}
		}
	}
	return edges
}

// GenerateGraph cria um HTML interativo (vis-network) com os URLs.
func GenerateGraph(urls []string, outputHTML string, maxNodes, clusterThreshold int) error {
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
		fmt.Fprintf(os.Stderr, "Warning: Limiting to %d nodes (from %d)\n", maxNodes, len(nodes))
		nodes = nodes[:maxNodes]
	}
	sort.Strings(nodes)

	edges := buildEdges(nodes)

	nodeID := make(map[string]int, len(nodes))
	for i, n := range nodes {
		nodeID[n] = i + 1
	}

	f, err := os.Create(outputHTML)
	if err != nil {
		return fmt.Errorf("error creating output file %s: %w", outputHTML, err)
	}
	defer f.Close()

	if err := writeHeader(f); err != nil {
		return err
	}
	if err := writeNodes(f, nodes); err != nil {
		return err
	}
	if err := writeEdges(f, edges, nodeID); err != nil {
		return err
	}
	if err := writeScript(f, nodes, clusterThreshold); err != nil {
		return err
	}

	fmt.Printf("Generated %s with %d nodes and %d edges\n", outputHTML, len(nodes), len(edges))
	return nil
}

func writeHeader(f *os.File) error {
	_, err := fmt.Fprint(f, `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>URL Graph Visualization</title>
<script type="text/javascript" src="https://unpkg.com/vis-network/standalone/umd/vis-network.min.js"></script>
<style type="text/css">
    :root {
        --bg: #f8f9fa; --panel-bg: white; --text: #333; --border: #ccc;
        --node-bg: #ffffff; --node-border: #999; --edge: #6c757d; --highlight: #dc3545;
    }
    body[data-theme="dark"] {
        --bg: #121212; --panel-bg: #1e1e1e; --text: #e0e0e0; --border: #444;
        --node-bg: #2d2d2d; --node-border: #555; --edge: #888; --highlight: #ff6b6b;
    }
    body {
        font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif;
        margin: 0; overflow: hidden; background: var(--bg); color: var(--text);
        transition: background 0.3s, color 0.3s;
    }
    #mynetwork { width: 100%; height: 100vh; background: var(--bg); }
    #controls {
        position: fixed; top: 10px; left: 10px; background: var(--panel-bg);
        padding: 16px; border-radius: 12px; box-shadow: 0 4px 20px rgba(0,0,0,0.15);
        z-index: 1000; display: flex; flex-wrap: wrap; gap: 10px;
        align-items: center; border: 1px solid var(--border);
    }
    #controls button, #controls select, #controls label {
        margin: 0; padding: 10px 14px; font-size: 14px; cursor: pointer;
        border-radius: 8px; border: 1px solid var(--border);
        background: var(--panel-bg); color: var(--text); transition: all 0.2s;
    }
    #controls button:hover, #controls select:hover {
        background: rgba(255,255,255,0.1); border-color: var(--highlight);
    }
    #theme-toggle { background: none; border: none; font-size: 20px; cursor: pointer; }
</style>
</head>
<body>
<div id="controls">
    <button onclick="clusterByDomain()">Cluster by Domain</button>
    <button onclick="clusterByHubsize()">Cluster by Hubsize</button>
    <select id="layout-select" onchange="changeLayout(this.value)">
        <option value="force">Force (Repulsão)</option>
        <option value="hierarchical">Hierárquico</option>
    </select>
    <label>
        <input type="checkbox" id="physics-toggle" checked onchange="togglePhysics(this.checked)"> Física
    </label>
    <button id="theme-toggle" onclick="toggleTheme()" title="Alternar tema">Light/Dark</button>
</div>
<div id="mynetwork"></div>
<script type="text/javascript">
`)
	return err
}

func writeNodes(f *os.File, nodes []string) error {
	if _, err := fmt.Fprint(f, "var nodes = new vis.DataSet([\n"); err != nil {
		return err
	}
	for i, n := range nodes {
		if i > 0 {
			if _, err := fmt.Fprint(f, ","); err != nil {
				return err
			}
		}
		u, _ := url.Parse(n)
		label := n
		if len(n) > 25 {
			parts := strings.Split(strings.TrimRight(u.Path, "/"), "/")
			if len(parts) > 1 {
				label = parts[len(parts)-1]
			} else {
				label = domainOf(n)
			}
			if len(label) > 12 {
				label = label[:12] + "..."
			}
		}
		label = strings.ReplaceAll(label, `"`, `\"`)
		title := strings.ReplaceAll(n, `"`, `\"`)
		if _, err := fmt.Fprintf(f,
			`{id: %d, label: "%s", group: "%s", title: "%s"}`,
			i+1, label, domainOf(n), title); err != nil {
			return fmt.Errorf("error writing node %s: %w", n, err)
		}
	}
	_, err := fmt.Fprintln(f, "]);")
	return err
}

func writeEdges(f *os.File, edges [][2]string, nodeID map[string]int) error {
	if _, err := fmt.Fprint(f, "var edges = new vis.DataSet([\n"); err != nil {
		return err
	}
	for i, e := range edges {
		if i > 0 {
			if _, err := fmt.Fprint(f, ","); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(f,
			`{from: %d, to: %d, arrows: {to: {enabled: true, scaleFactor: 0.3}}, smooth: {type: 'curved', roundness: 0.2}}`,
			nodeID[e[0]], nodeID[e[1]]); err != nil {
			return fmt.Errorf("error writing edge %v: %w", e, err)
		}
	}
	_, err := fmt.Fprintln(f, "]);")
	return err
}

func writeScript(f *os.File, nodes []string, clusterThreshold int) error {
	// Grupos (um por domínio registrável)
	domains := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		domains[domainOf(n)] = struct{}{}
	}
	domainList := make([]string, 0, len(domains))
	for d := range domains {
		domainList = append(domainList, d)
	}
	sort.Strings(domainList)

	colors := []string{
		"#FF6347", "#4682B4", "#32CD32", "#FFD700", "#6A5ACD",
		"#FF4500", "#20B2AA", "#9932CC", "#00CED1", "#FF69B4",
	}

	// Cabeçalho do JS + options até "groups:"
	if _, err := fmt.Fprint(f, `]);
var container = document.getElementById('mynetwork');
var data = { nodes: nodes, edges: edges };
var options = {
    physics: {
        enabled: true,
        barnesHut: {
            gravitationalConstant: -10000, centralGravity: 0.1,
            springLength: 500, springConstant: 0.04, damping: 0.9,
            avoidOverlap: 1.5
        },
        stabilization: { enabled: true, iterations: 4000, updateInterval: 50 }
    },
    nodes: {
        shape: 'dot', size: 20,
        font: { size: 22, face: 'Arial', color: 'var(--text)', strokeWidth: 2, strokeColor: 'var(--bg)' },
        borderWidth: 3,
        color: {
            background: 'var(--node-bg)', border: 'var(--node-border)',
            highlight: { background: 'var(--highlight)', border: '#fff' }
        }
    },
    edges: {
        color: { color: 'var(--edge)', highlight: 'var(--highlight)' },
        smooth: { type: 'continuous', roundness: 0.8 },
        arrows: { to: { enabled: true, scaleFactor: 0.3 } }
    },
    layout: {
        improvedLayout: true,
        hierarchical: {
            enabled: false, direction: 'UD', sortMethod: 'directed',
            levelSeparation: 500, nodeSpacing: 400, treeSpacing: 800
        }
    },
    interaction: {
        hover: true, zoomView: true, dragView: true,
        multiselect: true, tooltipDelay: 200
    },
    groups: {
`); err != nil {
		return err
	}

	for i, d := range domainList {
		bg := colors[i%len(colors)]
		border := colors[(i+1)%len(colors)]
		if i > 0 {
			if _, err := fmt.Fprint(f, ",\n"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(f,
			`        "%s": { color: { background: "%s", border: "%s", highlight: { background: "%s", border: "#fff" } } }`,
			d, bg, border, bg); err != nil {
			return err
		}
	}

	// Resto do script
	if _, err := fmt.Fprintf(f, `
    }
};
var network = new vis.Network(container, data, options);

network.on("stabilizationIterationsDone", function() {
    if (document.getElementById('physics-toggle').checked) {
        network.setOptions({ physics: { enabled: false } });
    }
});

function changeLayout(layout) {
    let phys = document.getElementById('physics-toggle').checked;
    if (layout === 'hierarchical') {
        network.setOptions({
            layout: { hierarchical: { enabled: true, direction: 'UD', sortMethod: 'directed',
                levelSeparation: 500, nodeSpacing: 400, treeSpacing: 800 } },
            physics: { enabled: false }
        });
    } else {
        network.setOptions({
            layout: { hierarchical: { enabled: false } },
            physics: {
                enabled: phys,
                barnesHut: { gravitationalConstant: -10000, springLength: 500, avoidOverlap: 1.5 }
            }
        });
        if (phys) network.startSimulation();
    }
}

function togglePhysics(enabled) {
    network.setOptions({ physics: { enabled: enabled } });
    if (enabled) network.startSimulation();
}

function clusterByDomain() {
    // Agrupa nós por .group (domínio) e cria um cluster por grupo
    var groups = {};
    nodes.forEach(function(n) {
        (groups[n.group] = groups[n.group] || []).push(n.id);
    });
    Object.keys(groups).forEach(function(g) {
        var ids = groups[g];
        if (ids.length < 2) return;
        network.cluster({
            joinCondition: function(nodeOptions) { return nodeOptions.group === g; },
            clusterNodeProperties: {
                label: g + ' (' + ids.length + ')',
                shape: 'box',
                color: (options.groups[g] && options.groups[g].color) || '#ccc'
            }
        });
    });
}

function clusterByHubsize() {
    network.clusterByHubsize(%d);
}

function toggleTheme() {
    var body = document.body;
    var current = body.getAttribute('data-theme');
    var next = current === 'dark' ? 'light' : 'dark';
    body.setAttribute('data-theme', next);
    document.getElementById('theme-toggle').innerHTML = next === 'dark' ? 'Light' : 'Dark';
}

if (nodes.length > %d) {
    clusterByDomain();
}

network.on("doubleClick", function(params) {
    if (params.nodes.length > 0 && network.isCluster(params.nodes[0])) {
        network.openCluster(params.nodes[0]);
    }
});

document.getElementById('layout-select').value = 'force';
</script>
</body>
</html>
`, 3, clusterThreshold); err != nil {
		return err
	}
	return nil
}
