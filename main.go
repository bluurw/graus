// grau.go / main.go
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
	"grau/internal/terminal"
	"grau/internal/urlutil"
)

const (
	Version    = "v10"
	MaxWorkers = 40
)

func main() {
	var (
		target, list, outputFile                             string
		urlExtensionsExclude, urlQueryStrings, urlExtensions string
		urlFilter                                            string
		urlEntrypoints, urlJsOnly, urlRepeat, urlIncomplete  bool
		urlAll, noTimeout, graphHTML, graphStatic, graphJS   bool
		urlRetry, domainDepth, urlMinBytes, urlMaxBytes      int
		maxURLs, globalTimeout, workers, maxNodes            int
		clusterThreshold                                     int
		jsLoose                                              bool
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
	flag.BoolVar(&graphHTML, "graph-html", false, "Generate HTML graph visualization")
	flag.BoolVar(&graphStatic, "graph-static", false, "Generate static PNG graph (requires graphviz)")
	flag.BoolVar(&graphJS, "graph-js", false, "Generate .js file with URL map")
	flag.IntVar(&maxNodes, "max-nodes", 10000, "Maximum number of nodes in graph")
	flag.IntVar(&clusterThreshold, "cluster-threshold", 50, "Minimum nodes per cluster")
	flag.BoolVar(&jsLoose, "js-loose", false, "Enable aggressive JS string extraction (more results, more noise)")
	flag.Parse()

	if noTimeout {
		globalTimeout = 0
	}

	if target == "" && list == "" {
		terminal.Art()
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n\n\n\n%s Examples:%s\n", terminal.Bold, terminal.Reset)
		fmt.Println(" Normal Crawler: go run . -t https://example.com")
		fmt.Println(" Entrypoints:    go run . -t https://example.com -url-entrypoints -o entrypoints.txt")
		fmt.Println(" JsOnly:         go run . -t https://example.com -url-jsOnly")
		fmt.Println(" Incomplete:     go run . -t https://example.com -url-incomplete")
		fmt.Println(" Fatal Scan:     go run . -l subdomains.txt -url-all -o endpoints.txt -workers 30 -no-timeout")
		fmt.Println(" Graph HTML:     go run . -l urls.txt -graph-html nome")
		fmt.Println(" Static Graph:   go run . -l urls.txt -graph-static nome")
		fmt.Println(" JS Map:         go run . -l urls.txt -graph-js nome")
		os.Exit(1)
	}

	// --- Coleta de start URLs e domínios permitidos ---
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

	// --- Filtros ---
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

	// --- Estado ---
	seen := make(map[string]struct{}, 500_000)
	discovered := make(map[string]struct{}, 500_000)
	printed := make(map[string]struct{}, 500_000)
	domainDepths := make(map[string]int, 1000)
	collectedURLs := make([]string, 0, 500_000)

	for _, u := range startURLs {
		if n := urlutil.Normalize(u, false); n != "" {
			seen[n] = struct{}{}
		}
	}

	// --- Saída persistente ---
	out, err := output.New(outputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sErro ao abrir arquivo de saída: %v%s\n", terminal.Red, err, terminal.Reset)
		os.Exit(1)
	}
	defer out.Close()

	// --- Contexto + sinal ---
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

	// --- Progress bar ---
	bar := progressbar.NewOptions(-1,
		progressbar.OptionSetDescription("Crawling"),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetWidth(15),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
		progressbar.OptionOnCompletion(func() { fmt.Fprint(os.Stderr, "\n") }),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer: "█", SaucerHead: ">", SaucerPadding: " ",
			BarStart: "[", BarEnd: "]",
		}),
	)

	// --- Workers ---
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

	// Fecha `results` quando todos os workers saírem
	go func() {
		wg.Wait()
		close(results)
	}()

	// --- Loop principal (produtor) ---
	var successCount, errorCount, linkCount int
	var lastURL string
	startTime := time.Now()

	go func() {
		defer close(tasks)
		queue := make([]string, 0, len(seen))
		for k := range seen {
			queue = append(queue, k)
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

			if terminal.ColorEnabled {
				printVerbose(urlItem, result)
			}

			for _, link := range result.Links {
				if !urlutil.InScope(link, allowedRoots, urlIncomplete) {
					continue
				}
				nlink := link
				key := urlutil.NormalizeKey(nlink, urlIncomplete || urlEntrypoints)
				collectedURLs = append(collectedURLs, nlink)

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
						out.WriteLine(nlink)
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

	// --- Grafos ---
	baseName := graphBaseName(flag.Args(), outputFile, graphHTML || graphStatic || graphJS)

	if graphHTML {
		file := baseName + "_graph.html"
		if err := graph.GenerateGraph(collectedURLs, file, maxNodes, clusterThreshold); err != nil {
			fmt.Fprintf(os.Stderr, "%sErro ao gerar grafo HTML: %v%s\n", terminal.Red, err, terminal.Reset)
			os.Exit(1)
		}
		fmt.Printf("Grafo HTML gerado: %s\n", file)
	}
	if graphStatic {
		file := baseName + "_static.png"
		if err := graph.GenerateStaticGraph(collectedURLs, file, maxNodes); err != nil {
			fmt.Fprintf(os.Stderr, "%sErro ao gerar grafo estático: %v%s\n", terminal.Red, err, terminal.Reset)
		} else {
			fmt.Printf("Grafo estático gerado: %s\n", file)
		}
	}
	if graphJS {
		file := baseName + "_map.js"
		if err := graph.GenerateJSMap(collectedURLs, file, maxNodes); err != nil {
			fmt.Fprintf(os.Stderr, "%sErro ao gerar mapa JS: %v%s\n", terminal.Red, err, terminal.Reset)
			os.Exit(1)
		}
		fmt.Printf("Mapa JS gerado: %s\n", file)
	}

	// --- Status final ---
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
	fmt.Fprintf(os.Stderr, "Stop Reason: %s\n", stopReason)
}

// ---------------------------------------------------------------------------
// Helpers locais do main
// ---------------------------------------------------------------------------

// splitCSV divide "a,b,c" em []string, removendo vazios. "" → nil.
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

// printVerbose imprime uma linha formatada por URL crawleada.
func printVerbose(urlItem string, r crawler.Result) {
	ts := terminal.Now()
	statusStr, statusColor := terminal.StatusColor(r.Status)
	if r.Err != nil {
		fmt.Fprintf(os.Stderr, "[%s][%sERROR%s] %s%s%s | %v\n",
			ts, terminal.Bold+terminal.Red, terminal.Reset,
			terminal.Bold, urlItem, terminal.Reset, r.Err)
		return
	}
	fmt.Fprintf(os.Stderr, "[%s][%s%s%s%s] %s%s%s%s | %dms | %s\n",
		ts,
		terminal.Bold, statusColor, statusStr, terminal.Reset,
		terminal.Bold, statusColor, urlItem, terminal.Reset,
		r.RT, r.CT)
}

// graphBaseName decide o prefixo dos arquivos de grafo.
//
// Prioridade: argumento posicional > -o (sem extensão) > "grau_graph".
func graphBaseName(args []string, outputFile string, graphEnabled bool) string {
	if graphEnabled && len(args) > 0 {
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
