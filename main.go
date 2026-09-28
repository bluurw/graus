package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/schollz/progressbar/v3"

	"grau/internal/crawler"
	"grau/internal/graph"
	"grau/internal/output"
	"grau/internal/ratelimit"
	"grau/internal/terminal"
	"grau/internal/urlutil"
)

const (
	Version    = "v11"
	MaxWorkers = 40
)

func main() {
	var (
		target, list, outputFile                             string
		urlExtensionsExclude, urlQueryStrings, urlExtensions string
		urlFilter, proxyURL                                  string
		headerFlags                                          multiFlag
		urlEntrypoints, urlJsOnly, urlRepeat, urlIncomplete  bool
		urlAll, noTimeout, graphHTML, graphStatic, graphJS   bool
		urlRetry, domainDepth, urlMinBytes, urlMaxBytes      int
		maxURLs, globalTimeout, workers, maxNodes            int
		clusterThreshold, hubsize                            int
		jsLoose, quiet, jsonOut, robots, showVersion, yolo   bool
		rateLimit                                            float64
		rateBurst                                            int
	)

	flag.StringVar(&target, "t", "", "Target URL")
	flag.StringVar(&list, "l", "", "File with URLs")
	flag.StringVar(&outputFile, "o", "", "Output file")
	flag.IntVar(&urlRetry, "url-retry", 2, "Retries per URL")
	flag.StringVar(&urlExtensionsExclude, "url-extensions-exclude", "", "Exclude extensions")
	flag.IntVar(&domainDepth, "domain-depth", 500, "Max URLs per domain")
	flag.BoolVar(&urlEntrypoints, "url-entrypoints", false, "Only URLs with ?key=value")
	flag.BoolVar(&urlJsOnly, "url-jsOnly", false, "Only .js files")
	flag.StringVar(&urlQueryStrings, "qs", "", "Query strings to match")
	flag.IntVar(&urlMinBytes, "url-minBytes", 0, "Min response size")
	flag.IntVar(&urlMaxBytes, "url-maxBytes", -1, "Max response size")
	flag.IntVar(&maxURLs, "max-urls", 1_000_000, "Max total URLs to crawl")
	flag.IntVar(&globalTimeout, "global-timeout", 3600, "Global timeout (seconds, 0 = unlimited)")
	flag.BoolVar(&urlRepeat, "url-repeat", false, "Allow repeated URLs (default: dedup)")
	flag.StringVar(&urlExtensions, "url-extensions", "", "Allowed extensions")
	flag.StringVar(&urlFilter, "url-filter", "", "String filter in URL")
	flag.BoolVar(&urlIncomplete, "url-incomplete", false, "Allow incomplete URLs (relative paths)")
	flag.BoolVar(&urlAll, "url-all", false, "Return ALL URLs (no filter)")
	flag.BoolVar(&noTimeout, "no-timeout", false, "Disable global timeout")
	flag.IntVar(&workers, "workers", MaxWorkers, "Concurrent workers")
	flag.BoolVar(&terminal.ColorEnabled, "color", false, "Enable color output")
	flag.BoolVar(&verbose, "v", false, "Verbose mode (status per URL)")
	flag.BoolVar(&graphHTML, "graph-html", false, "Generate HTML graph visualization")
	flag.BoolVar(&graphStatic, "graph-static", false, "Generate static PNG graph (requires graphviz)")
	flag.BoolVar(&graphJS, "graph-js", false, "Generate .js file with URL map")
	flag.IntVar(&maxNodes, "max-nodes", 10000, "Maximum number of nodes in graph")
	flag.IntVar(&clusterThreshold, "cluster-threshold", 50, "Min nodes to auto-cluster")
	flag.IntVar(&hubsize, "hubsize", 5, "clusterByHubsize threshold")
	flag.BoolVar(&jsLoose, "js-loose", false, "Aggressive JS string extraction")

	// NOVAS
	flag.BoolVar(&quiet, "q", false, "Quiet: suppress stdout URLs (only file output)")
	flag.BoolVar(&jsonOut, "json", false, "Output one JSON object per line")
	flag.BoolVar(&robots, "robots", true, "Fetch robots.txt and sitemap.xml")
	flag.StringVar(&proxyURL, "proxy", "", "HTTP/SOCKS proxy URL")
	flag.Float64Var(&rateLimit, "rate-limit", 5, "Max requests per second per host (0 = unlimited)")
	flag.IntVar(&rateBurst, "rate-burst", 10, "Burst for rate limiter")
	flag.Var(&headerFlags, "H", "Custom header (e.g. -H 'Authorization: Bearer x')")
	flag.BoolVar(&showVersion, "version", false, "Print version and exit")
	flag.BoolVar(&yolo, "yolo", false, "Aggressive mode: enables -url-all -js-loose -no-timeout -robots -rate-limit 0")

	flag.Parse()

	if yolo {
		// Aviso interativo: exige confirmação explícita
		fmt.Fprintf(os.Stderr, "%s[!] Modo YOLO ativado:%s\n", terminal.Yellow, terminal.Reset)
		fmt.Fprintf(os.Stderr, "    - Sem rate limit, sem timeout, workers no máximo\n")
		fmt.Fprintf(os.Stderr, "    - Só use em alvo que você tem autorização escrita\n")
		if !confirmYolo() {
			fmt.Fprintln(os.Stderr, "Cancelado.")
			os.Exit(1)
		}

		urlAll = true
		jsLoose = true
		noTimeout = true
		robots = true
		rateLimit = 0
		workers = MaxWorkers
	}

	if showVersion {
		fmt.Println("grau", Version)
		os.Exit(0)
	}

	if noTimeout {
		globalTimeout = 0
	}

	if target == "" && list == "" {
		terminal.Art()
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n\n%s Examples:%s\n", terminal.Bold, terminal.Reset)
		fmt.Println(" Normal Crawler: go run . -t https://example.com")
		fmt.Println(" Entrypoints:    go run . -t https://example.com -url-entrypoints")
		fmt.Println(" JsOnly:         go run . -t https://example.com -url-jsOnly")
		fmt.Println(" Incomplete:     go run . -t https://example.com -url-incomplete")
		fmt.Println(" JS Loose:       go run . -t https://example.com -js-loose")
		fmt.Println(" JSON output:    go run . -t https://example.com -json")
		fmt.Println(" Graph HTML:     go run . -l urls.txt -graph-html nome")
		os.Exit(1)
	}

	// Construir client (proxy)
	if err := crawler.BuildClient(proxyURL); err != nil {
		fmt.Fprintf(os.Stderr, "%sErro: %v%s\n", terminal.Red, err, terminal.Reset)
		os.Exit(1)
	}

	// Headers customizados
	customHeaders := parseHeaders(headerFlags)

	// Coleta start URLs
	startURLs := []string{}
	allowedRoots := make(map[string]struct{}, 100)
	if list != "" {
		f, err := os.Open(list)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%sErro ao abrir arquivo: %v%s\n", terminal.Red, err, terminal.Reset)
			os.Exit(1)
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for sc.Scan() {
			u := strings.TrimSpace(sc.Text())
			if u == "" || strings.HasPrefix(u, "#") {
				continue
			}
			if !strings.HasPrefix(u, "http") {
				u = "https://" + u
			}
			startURLs = append(startURLs, u)
			if p, _ := url.Parse(u); p != nil {
				if d := urlutil.DomainFromURL(p); d != "" {
					allowedRoots[urlutil.RootDomain(d)] = struct{}{}
				}
			}
		}
	} else {
		startURLs = []string{target}
		if p, _ := url.Parse(target); p != nil {
			if d := urlutil.DomainFromURL(p); d != "" {
				allowedRoots[urlutil.RootDomain(d)] = struct{}{}
			}
		}
	}

	filters := urlutil.Filters{
		EntryPoints: urlEntrypoints,
		JSOnly:      urlJsOnly,
		All:         urlAll,
		Incomplete:  urlIncomplete,
		QueryList:   splitCSV(urlQueryStrings),
		ExcludeExt:  splitCSV(urlExtensionsExclude),
		AllowedExt:  splitCSV(urlExtensions),
		FilterStr:   strings.ToLower(urlFilter),
	}

	seen := make(map[string]struct{}, 500_000)
	discovered := make(map[string]struct{}, 500_000)
	printed := make(map[string]struct{}, 500_000)
	domainDepths := make(map[string]int, 1000)

	// NOVO: dedup de collectedURLs
	collectedSet := make(map[string]struct{}, 500_000)
	collectedURLs := make([]string, 0, 500_000)
	metaByURL := make(map[string]graph.NodeMeta, 500_000)

	for _, u := range startURLs {
		if n := urlutil.Normalize(u, false); n != "" {
			seen[n] = struct{}{}
		}
	}

	out, err := output.New(outputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sErro ao abrir arquivo: %v%s\n", terminal.Red, err, terminal.Reset)
		os.Exit(1)
	}
	out.Quiet = quiet
	out.JSON = jsonOut
	defer out.Close()

	var baseCtx context.Context
	var cancel context.CancelFunc
	if globalTimeout > 0 {
		baseCtx, cancel = context.WithTimeout(context.Background(), time.Duration(globalTimeout)*time.Second)
	} else {
		baseCtx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()

	ctx, stop := signal.NotifyContext(baseCtx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	bar := progressbar.NewOptions(-1,
		progressbar.OptionSetDescription("Crawling"),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetWidth(15),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
		progressbar.OptionShowBytes(false),
		progressbar.OptionOnCompletion(func() { fmt.Fprint(os.Stderr, "\n") }),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer: "█", SaucerHead: ">", SaucerPadding: " ",
			BarStart: "[", BarEnd: "]",
		}),
	)

	limiter := ratelimit.New(rateLimit, rateBurst)

	tasks := make(chan string, workers*2)
	results := make(chan crawler.Result, workers)
	var wg sync.WaitGroup
	opts := crawler.Options{
		Retries:     urlRetry,
		MinBytes:    urlMinBytes,
		MaxBytes:    urlMaxBytes,
		Incomplete:  urlIncomplete,
		EntryPoints: urlEntrypoints,
		JSLoose:     jsLoose,
		Limiter:     limiter,
		Headers:     customHeaders,
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range tasks {
				if ctx.Err() != nil {
					return
				}
				res := crawler.Fetch(ctx, u, opts)
				select {
				case results <- res:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var successCount, errorCount, linkCount int
	var lastURL string
	startTime := time.Now()

	go func() {
		defer close(tasks)
		queue := make([]string, 0, len(seen))
		for k := range seen {
			queue = append(queue, k)
		}

		// NOVO: robots/sitemap na primeira passada
		if robots {
			for host := range allowedRoots {
				// best effort: tenta https
				wk := crawler.WellKnown(ctx, host, urlIncomplete, jsLoose)
				for _, u := range wk {
					if _, ok := seen[u]; !ok {
						seen[u] = struct{}{}
						queue = append(queue, u)
						if _, dup := collectedSet[u]; !dup {
							collectedSet[u] = struct{}{}
							collectedURLs = append(collectedURLs, u)
						}
					}
				}
			}
		}

		for len(queue) > 0 && ctx.Err() == nil {
			urlItem := queue[0]
			queue = queue[1:]
			lastURL = urlItem

			u, _ := url.Parse(urlItem)
			if u == nil {
				continue
			}
			domain := urlutil.DomainFromURL(u)
			if domain != "" && domainDepths[domain] >= domainDepth {
				continue
			}
			if successCount+errorCount >= maxURLs {
				break
			}

			select {
			case tasks <- urlItem:
			case <-ctx.Done():
				return
			}

			var result crawler.Result
			select {
			case result = <-results:
			case <-ctx.Done():
				return
			}

			if result.Err != nil {
				errorCount++
			} else {
				successCount++
				if domain != "" {
					domainDepths[domain]++
				}
			}
			bar.Add(1)

			// Guarda meta do próprio urlItem (agora que sabemos status/CT)
			if _, dup := collectedSet[urlItem]; !dup {
				collectedSet[urlItem] = struct{}{}
				collectedURLs = append(collectedURLs, urlItem)
			}
			metaByURL[urlItem] = graph.NodeMeta{URL: urlItem, Status: result.Status, CT: result.CT}

			// NOVO: -v funciona com ou sem -color
			if verbose || terminal.ColorEnabled {
				printVerbose(urlItem, result)
			}

			for _, link := range result.Links {
				if !urlutil.InScope(link, allowedRoots, urlIncomplete) {
					continue
				}
				nlink := link
				key := urlutil.NormalizeKey(nlink, urlIncomplete || urlEntrypoints)

				// Dedup em collected
				if _, dup := collectedSet[nlink]; !dup {
					collectedSet[nlink] = struct{}{}
					collectedURLs = append(collectedURLs, nlink)
				}

				printKey := key
				if urlJsOnly || urlEntrypoints {
					printKey = nlink
				}
				if _, ok := discovered[key]; !ok {
					linkCount++
					discovered[key] = struct{}{}
				}

				if urlutil.Pass(nlink, filters) {
					printIt := urlRepeat
					if !urlRepeat {
						if _, ok := printed[printKey]; !ok {
							printIt = true
						}
					}
					if printIt {
						out.WriteRecord(output.Record{URL: nlink, Status: result.Status, CT: result.CT, RTms: result.RT, Source: "crawl"})
						printed[printKey] = struct{}{}
					}
				}

				if !urlIncomplete && !urlEntrypoints && !urlJsOnly {
					shouldQueue := urlRepeat
					if !urlRepeat {
						if _, ok := seen[nlink]; !ok {
							shouldQueue = true
						}
					}
					if shouldQueue {
						if p, err := url.Parse(nlink); err == nil {
							ld := urlutil.DomainFromURL(p)
							if ld != "" && domainDepths[ld] < domainDepth {
								seen[nlink] = struct{}{}
								queue = append(queue, nlink)
							}
						}
					}
				}
			}
		}
	}()

	wg.Wait()
	bar.Finish()
	out.Flush()

	baseName := graphBaseName(flag.Args(), outputFile, graphHTML || graphStatic || graphJS)

	if graphHTML {
		file := baseName + "_graph.html"
		gopt := graph.Options{
			MaxNodes:         maxNodes,
			ClusterThreshold: clusterThreshold,
			Hubsize:          hubsize,
			Metas:            metaByURL,
		}
		if err := graph.GenerateGraph(collectedURLs, file, gopt); err != nil {
			fmt.Fprintf(os.Stderr, "%sErro ao gerar grafo HTML: %v%s\n", terminal.Red, err, terminal.Reset)
			os.Exit(1)
		}
		fmt.Printf("Grafo HTML gerado: %s\n", file)
	}
	if graphStatic {
		file := baseName + "_static.png"
		if err := graph.GenerateStaticGraph(collectedURLs, file, maxNodes); err != nil {
			fmt.Fprintf(os.Stderr, "%sErro: %v%s\n", terminal.Red, err, terminal.Reset)
		} else {
			fmt.Printf("Grafo estático gerado: %s\n", file)
		}
	}
	if graphJS {
		file := baseName + "_map.js"
		if err := graph.GenerateJSMap(collectedURLs, file, maxNodes); err != nil {
			fmt.Fprintf(os.Stderr, "%sErro: %v%s\n", terminal.Red, err, terminal.Reset)
			os.Exit(1)
		}
		fmt.Printf("Mapa JS gerado: %s\n", file)
	}

	stopReason := "completed"
	interrupted := false
	switch {
	case ctx.Err() == nil && successCount+errorCount >= maxURLs:
		stopReason = "max-urls"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		stopReason = "timeout"
	case errors.Is(ctx.Err(), context.Canceled):
		stopReason = "interrupted"
		interrupted = true
	}
	if interrupted {
		fmt.Fprintf(os.Stderr, "\n[!] Crawling interrompido pelo usuário\n")
	}

	fmt.Fprintf(os.Stderr, "\n=== GRAU CRAWL SUMMARY ===\n")
	fmt.Fprintf(os.Stderr, "Time: %s\n", terminal.Duration(time.Since(startTime)))
	fmt.Fprintf(os.Stderr, "URLs Crawled: %d\n", successCount+errorCount)
	fmt.Fprintf(os.Stderr, "Last URL: %s\n", lastURL)
	fmt.Fprintf(os.Stderr, "Success: %d\n", successCount)
	fmt.Fprintf(os.Stderr, "Failed: %d\n", errorCount)
	fmt.Fprintf(os.Stderr, "Links Found: %d\n", linkCount)
	fmt.Fprintf(os.Stderr, "Unique URLs: %d\n", len(collectedURLs))
	fmt.Fprintf(os.Stderr, "Stop Reason: %s\n", stopReason)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// multiFlag permite -H múltiplas vezes.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// parseHeaders converte ["Authorization: Bearer x"] em map.
func parseHeaders(hs []string) map[string]string {
	if len(hs) == 0 {
		return nil
	}
	m := make(map[string]string, len(hs))
	for _, h := range hs {
		i := strings.Index(h, ":")
		if i <= 0 {
			continue
		}
		k := strings.TrimSpace(h[:i])
		v := strings.TrimSpace(h[i+1:])
		if k != "" {
			m[k] = v
		}
	}
	return m
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(strings.ToLower(s), ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// verboseFlag é global e mutável (a flag aponta pro endereço).
var verbose bool

func printVerbose(urlItem string, r crawler.Result) {
	ts := terminal.Now()
	statusStr, statusColor := terminal.StatusColor(r.Status)
	if r.Err != nil {
		fmt.Fprintf(os.Stderr, "[%s][ERROR] %s | %v\n", ts, urlItem, r.Err)
		return
	}
	if terminal.ColorEnabled {
		fmt.Fprintf(os.Stderr, "[%s][%s%s%s%s] %s%s%s%s | %dms | %s\n",
			ts, terminal.Bold, statusColor, statusStr, terminal.Reset,
			terminal.Bold, statusColor, urlItem, terminal.Reset,
			r.RT, r.CT)
	} else {
		fmt.Fprintf(os.Stderr, "[%s][%s] %s | %dms | %s\n", ts, statusStr, urlItem, r.RT, r.CT)
	}
}

func graphBaseName(args []string, outputFile string, enabled bool) string {
	if enabled && len(args) > 0 {
		name := args[0]
		if ext := filepath.Ext(name); ext != "" {
			name = strings.TrimSuffix(name, ext)
		}
		return name
	}
	if outputFile != "" {
		return strings.TrimSuffix(outputFile, filepath.Ext(outputFile))
	}
	return "grau_graph"
}

// confirmYolo pede confirmação interativa antes de rodar em modo agressivo.
//
// Aceita "y", "yes" ou "s" (case-insensitive). Qualquer outra coisa cancela.
func confirmYolo() bool {
	fmt.Fprint(os.Stderr, "\n  Continuar? [y/N] ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes" || line == "s"
}
