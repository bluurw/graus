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
	"grau/internal/passive"
	"grau/internal/queue"
	"grau/internal/ratelimit"
	"grau/internal/terminal"
	"grau/internal/urlutil"
)

const (
	Version    = "v17"
	MaxWorkers = 40
)

func main() {
	var (
		target, list, outputFile                             string
		urlExtensionsExclude, urlQueryStrings, urlExtensions string
		urlFilter, proxyURL, outDir                          string
		headerFlags                                          multiFlag
		urlEntrypoints, urlJsOnly, urlRepeat, urlIncomplete  bool
		urlAll, noTimeout, graphHTML, graphStatic, graphJS   bool
		urlRetry, domainDepth, urlMinBytes, urlMaxBytes      int
		maxURLs, globalTimeout, workers, maxNodes            int
		clusterThreshold, hubsize                            int
		jsLoose, quiet, jsonOut, robots, showVersion         bool
		yolo, assumeYes, details                             bool
		headless, sourceMap, deep                            bool
		includeSubdomains, prioritize, backpressure          bool
		excludeHosts                                         multiFlag
		scopeRegex                                           string
		rateLimit                                            float64
		rateBurst                                            int
		passiveAll, passiveWayback, passiveCC                bool
		passiveOTX, passiveURLScan                           bool
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
	flag.IntVar(&globalTimeout, "global-timeout", 3600, "Global timeout (seconds)")
	flag.BoolVar(&urlRepeat, "url-repeat", false, "Allow repeated URLs")
	flag.StringVar(&urlExtensions, "url-extensions", "", "Allowed extensions")
	flag.StringVar(&urlFilter, "url-filter", "", "String filter in URL")
	flag.BoolVar(&urlIncomplete, "url-incomplete", false, "Allow relative URLs")
	flag.BoolVar(&urlAll, "url-all", false, "Return ALL URLs")
	flag.BoolVar(&noTimeout, "no-timeout", false, "Disable global timeout")
	flag.IntVar(&workers, "workers", MaxWorkers, "Concurrent workers")
	flag.BoolVar(&terminal.ColorEnabled, "color", false, "Enable color output")
	flag.BoolVar(&verbose, "v", false, "Verbose mode")
	flag.BoolVar(&details, "details", false, "Details: one line per URL with source and time")
	flag.BoolVar(&graphHTML, "graph-html", false, "Generate HTML graph")
	flag.BoolVar(&graphStatic, "graph-static", false, "Generate static PNG")
	flag.BoolVar(&graphJS, "graph-js", false, "Generate JS map")
	flag.IntVar(&maxNodes, "max-nodes", 10000, "Max nodes in graph")
	flag.IntVar(&clusterThreshold, "cluster-threshold", 50, "Auto-cluster threshold")
	flag.IntVar(&hubsize, "hubsize", 5, "clusterByHubsize threshold")
	flag.BoolVar(&jsLoose, "js-loose", false, "Aggressive JS extraction")
	flag.BoolVar(&quiet, "q", false, "Quiet stdout")
	flag.BoolVar(&jsonOut, "json", false, "JSON lines output")
	flag.BoolVar(&robots, "robots", true, "Fetch robots.txt/sitemap.xml")
	flag.StringVar(&proxyURL, "proxy", "", "HTTP/SOCKS proxy")
	flag.Float64Var(&rateLimit, "rate-limit", 5, "Requests/sec per host")
	flag.IntVar(&rateBurst, "rate-burst", 10, "Burst")
	flag.Var(&headerFlags, "H", "Custom header")
	flag.BoolVar(&showVersion, "version", false, "Print version")
	flag.BoolVar(&yolo, "yolo", false, "Aggressive mode")
	flag.BoolVar(&assumeYes, "yes", false, "Skip interactive confirmation")

	flag.BoolVar(&headless, "headless", false, "Use headless Chrome")
	flag.BoolVar(&sourceMap, "sourcemap", false, "Fetch .map files")
	flag.BoolVar(&deep, "deep", false, "Extract forms, JSON keys, headers")

	flag.StringVar(&outDir, "out-dir", "", "Directory for segmented output")
	flag.String("out-urls", "", "(reserved)")
	flag.String("out-params", "", "(reserved)")
	flag.String("out-js", "", "(reserved)")
	flag.String("out-xhr", "", "(reserved)")
	flag.String("out-forms", "", "(reserved)")
	flag.String("out-keys", "", "(reserved)")
	flag.String("out-headers", "", "(reserved)")
	flag.String("out-sourcemaps", "", "(reserved)")

	flag.BoolVar(&includeSubdomains, "include-subdomains", false, "Include any subdomain")
	flag.Var(&excludeHosts, "exclude-host", "Host to exclude")
	flag.StringVar(&scopeRegex, "scope-regex", "", "Only accept URLs matching this regex")

	flag.BoolVar(&prioritize, "prioritize", false, "Prioritize URLs with params/JS/api")
	flag.BoolVar(&backpressure, "backpressure", false, "Slow down 429/503 hosts")

	flag.BoolVar(&passiveAll, "passive-all", false, "Enable all passive sources")
	flag.BoolVar(&passiveWayback, "passive-wayback", false, "Wayback Machine")
	flag.BoolVar(&passiveCC, "passive-cc", false, "Common Crawl")
	flag.BoolVar(&passiveOTX, "passive-otx", false, "AlienVault OTX")
	flag.BoolVar(&passiveURLScan, "passive-urlscan", false, "URLScan.io")

	flag.Parse()

	passiveExplicit := map[string]bool{}
	flag.Visit(func(f *flag.Flag) {
		if strings.HasPrefix(f.Name, "passive-") {
			passiveExplicit[f.Name] = true
		}
	})
	anyPassiveExplicit := len(passiveExplicit) > 0

	if yolo {
		fmt.Fprintf(os.Stderr, "%s[!] Modo YOLO ativado:%s\n", terminal.Yellow, terminal.Reset)
		if !assumeYes && !confirmYolo() {
			fmt.Fprintln(os.Stderr, "Cancelado.")
			os.Exit(1)
		}
		urlAll = true
		jsLoose = true
		noTimeout = true
		robots = true
		rateLimit = 0
		workers = MaxWorkers
		deep = true
		sourceMap = true
		includeSubdomains = true
		prioritize = true
		backpressure = true

		if !anyPassiveExplicit {
			passiveAll = true
		}
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
		os.Exit(1)
	}

	if err := crawler.BuildClient(proxyURL); err != nil {
		fmt.Fprintf(os.Stderr, "%sErro: %v%s\n", terminal.Red, err, terminal.Reset)
		os.Exit(1)
	}

	if headless && !crawler.ChromeAvailable() {
		fmt.Fprintf(os.Stderr, "%s[!] --headless pedido, mas Chrome/Chromium não encontrado. Ignorando.%s\n", terminal.Yellow, terminal.Reset)
		headless = false
	}

	customHeaders := parseHeaders(headerFlags)

	startURLs := []string{}
	allowedRoots := make(map[string]struct{}, 100)
	if list != "" {
		f, err := os.Open(list)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%sErro: %v%s\n", terminal.Red, err, terminal.Reset)
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

	// Cabeçalho de sessão
	for root := range allowedRoots {
		fmt.Fprintf(os.Stderr, "%s[aggressive]%s graus → %s (session start)\n", terminal.Yellow, terminal.Reset, root)
	}

	// Lista de roots fixa — nunca muda depois do boot.
	// Só aceitamos URLs cujo root domain esteja nesta lista.
	scopeRoots := make([]string, 0, len(allowedRoots))
	for root := range allowedRoots {
		scopeRoots = append(scopeRoots, root)
	}

	// hostInScope verifica se o host pertence a um dos roots conhecidos
	// (host é igual ao root ou subdomínio dele). NUNCA adiciona.
	hostInScope := func(host string) bool {
		host = strings.ToLower(host)
		if host == "" {
			return false
		}
		for _, root := range scopeRoots {
			if host == root || strings.HasSuffix(host, "."+root) {
				return true
			}
		}
		return false
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
		fmt.Fprintf(os.Stderr, "%sErro: %v%s\n", terminal.Red, err, terminal.Reset)
		os.Exit(1)
	}
	out.Quiet = quiet
	out.JSON = jsonOut
	defer out.Close()

	var seg *output.SegmentedWriter
	if outDir != "" {
		seg, err = output.NewSegmented(outDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%sErro ao criar out-dir: %v%s\n", terminal.Red, err, terminal.Reset)
			os.Exit(1)
		}
		defer seg.Close()
	}

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
	bp := ratelimit.NewBackpressure(backpressure)

	opts := crawler.Options{
		Retries:           urlRetry,
		MinBytes:          urlMinBytes,
		MaxBytes:          urlMaxBytes,
		Incomplete:        urlIncomplete,
		EntryPoints:       urlEntrypoints,
		JSLoose:           jsLoose,
		Limiter:           limiter,
		Headers:           customHeaders,
		Headless:          headless,
		SourceMap:         sourceMap,
		Deep:              deep,
		IncludeSubdomains: includeSubdomains,
		ExcludeHosts:      excludeHosts,
		ScopeRegex:        scopeRegex,
		Prioritize:        prioritize,
		Backpressure:      backpressure,
		BP:                bp,
	}
	if err := opts.Compile(); err != nil {
		fmt.Fprintf(os.Stderr, "%sErro no scope-regex: %v%s\n", terminal.Red, err, terminal.Reset)
		os.Exit(1)
	}

	tasks := make(chan string, workers*2)
	results := make(chan crawler.Result, workers)
	deepCh := make(chan crawler.DeepFinding, workers*4)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for u := range tasks {
				if ctx.Err() != nil {
					return
				}
				res, findings := crawler.FetchDeep(ctx, u, opts)
				for _, f := range findings {
					select {
					case deepCh <- f:
					case <-ctx.Done():
						return
					}
				}
				select {
				case results <- res:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	var deepCounters struct {
		sync.Mutex
		xhr, form, key, header, sourcemap int
	}
	var deepWG sync.WaitGroup
	deepWG.Add(1)
	go func() {
		defer deepWG.Done()
		for f := range deepCh {
			if seg != nil {
				switch f.Kind {
				case "xhr":
					_ = seg.WriteXHR(f.Data)
				case "form":
					_ = seg.WriteForm(f.URL, f.Data)
				case "json_key":
					_ = seg.WriteKey(f.Data)
				case "header":
					_ = seg.WriteHeader(f.Data)
				case "sourcemap":
					_ = seg.WriteSourceMap(f.Data)
				}
			}
			deepCounters.Lock()
			switch f.Kind {
			case "xhr":
				deepCounters.xhr++
			case "form":
				deepCounters.form++
			case "json_key":
				deepCounters.key++
			case "header":
				deepCounters.header++
			case "sourcemap":
				deepCounters.sourcemap++
			}
			deepCounters.Unlock()
		}
	}()

	go func() {
		wg.Wait()
		close(results)
		close(deepCh)
	}()

	var successCount, errorCount, linkCount, passiveCount int
	var lastURL string
	startTime := time.Now()
	lastHost := ""

	go func() {
		defer close(tasks)

		var pq *queue.PriorityQueue
		var fifo []string
		if prioritize {
			pq = queue.New()
		}

		enqueue := func(u string) {
			if prioritize {
				pq.Enqueue(u)
			} else {
				fifo = append(fifo, u)
			}
		}
		dequeue := func() (string, bool) {
			if prioritize {
				return pq.Dequeue()
			}
			if len(fifo) == 0 {
				return "", false
			}
			u := fifo[0]
			fifo = fifo[1:]
			return u, true
		}

		for _, u := range startURLs {
			if n := urlutil.Normalize(u, false); n != "" {
				enqueue(n)
			}
		}

		if robots {
			for root := range allowedRoots {
				wk := crawler.WellKnown(ctx, root, urlIncomplete, jsLoose)
				for _, u := range wk {
					if _, ok := seen[u]; !ok {
						seen[u] = struct{}{}
						enqueue(u)
						if _, dup := collectedSet[u]; !dup {
							collectedSet[u] = struct{}{}
							collectedURLs = append(collectedURLs, u)
						}
					}
				}
			}
		}

		// --- Fontes passivas ---
		if passiveAll {
			passiveWayback = true
			passiveCC = true
			passiveOTX = true
			passiveURLScan = true
		}
		passiveSources := passive.All(passive.Options{
			Wayback: passiveWayback,
			CC:      passiveCC,
			OTX:     passiveOTX,
			URLScan: passiveURLScan,
			Timeout: 30 * time.Second,
		})
		if len(passiveSources) > 0 {
			for root := range allowedRoots {
				for _, src := range passiveSources {
					fmt.Fprintf(os.Stderr, "%s[passive]%s %s → %s\n", terminal.Cyan, terminal.Reset, src.Name(), root)
					urls, err := src.Fetch(ctx, root)
					if err != nil {
						fmt.Fprintf(os.Stderr, "%s[passive]%s %s error: %v\n", terminal.Cyan, terminal.Reset, src.Name(), err)
						continue
					}
					added := 0
					for _, u := range urls {
						n := urlutil.Normalize(u, false)
						if n == "" || urlutil.LooksTruncated(n) {
							continue
						}
						// Verifica se o host está no escopo ANTES de qualquer coisa
						if p, err := url.Parse(n); err == nil {
							host := urlutil.DomainFromURL(p)
							if !hostInScope(host) {
								continue
							}
						} else {
							continue
						}
						if _, dup := printed[n]; !dup {
							out.WriteRecord(output.Record{URL: n, Source: "passive:" + src.Name()})
							if seg != nil {
								_ = seg.WriteURL(n)
								if urlutil.HasValidEntrypoint(n) {
									_ = seg.WriteParam(n)
								}
								if strings.HasSuffix(strings.ToLower(n), ".js") {
									_ = seg.WriteJS(n)
								}
							}
							if details {
								fmt.Fprintf(os.Stderr, "%s[passive]%s[%s] %s\n",
									terminal.Cyan, terminal.Reset, time.Now().Format("15:04:05"), n)
							}
							printed[n] = struct{}{}
						}
						if _, ok := seen[n]; ok {
							continue
						}
						seen[n] = struct{}{}
						passiveCount++
						added++
						if _, dup := collectedSet[n]; !dup {
							collectedSet[n] = struct{}{}
							collectedURLs = append(collectedURLs, n)
						}
						enqueue(n)
					}
					fmt.Fprintf(os.Stderr, "[passive] %s: %d URLs\n", src.Name(), added)
				}
			}
		}

		// Cabeçalho do crawl ativo (depois das passivas)
		for root := range allowedRoots {
			fmt.Fprintf(os.Stderr, "%s[aggressive]%s crawl → %s (starting)\n", terminal.Yellow, terminal.Reset, root)
		}

		for {
			if ctx.Err() != nil {
				return
			}
			urlItem, ok := dequeue()
			if !ok {
				return
			}
			lastURL = urlItem

			u, _ := url.Parse(urlItem)
			if u == nil {
				continue
			}
			domain := urlutil.DomainFromURL(u)

			if domain != "" && domain != lastHost {
				if lastHost != "" {
					fmt.Fprintf(os.Stderr, "%s[aggressive]%s host change: %s → %s\n", terminal.Yellow, terminal.Reset, lastHost, domain)
				}
				lastHost = domain
			}

			if opts.HostExcluded(domain) {
				continue
			}
			if opts.ScopeRegex != "" && !opts.ScopeOK(urlItem) {
				continue
			}

			if domain != "" && domainDepths[domain] >= domainDepth {
				continue
			}
			if successCount+errorCount >= maxURLs {
				break
			}

			if details {
				fmt.Fprintf(os.Stderr, "%s[aggressive]%s[%s] %s\n",
					terminal.Yellow, terminal.Reset, time.Now().Format("15:04:05"), urlItem)
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

			if _, dup := collectedSet[urlItem]; !dup {
				collectedSet[urlItem] = struct{}{}
				collectedURLs = append(collectedURLs, urlItem)
			}
			metaByURL[urlItem] = graph.NodeMeta{URL: urlItem, Status: result.Status, CT: result.CT}

			if verbose || terminal.ColorEnabled {
				printVerbose(urlItem, result)
			}

			for _, link := range result.Links {
				if urlutil.LooksTruncated(link) {
					continue
				}
				// Verifica host ANTES de qualquer escrita
				if p, err := url.Parse(link); err == nil {
					host := urlutil.DomainFromURL(p)
					if !hostInScope(host) {
						continue
					}
				} else {
					continue
				}
				nlink := link
				key := urlutil.NormalizeKey(nlink, urlIncomplete || urlEntrypoints)

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
						if seg != nil {
							_ = seg.WriteURL(nlink)
							if urlutil.HasValidEntrypoint(nlink) {
								_ = seg.WriteParam(nlink)
							}
							if strings.HasSuffix(strings.ToLower(nlink), ".js") {
								_ = seg.WriteJS(nlink)
							}
						}
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
								enqueue(nlink)
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
	if seg != nil {
		seg.Flush()
	}
	deepWG.Wait()

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
	if passiveCount > 0 {
		fmt.Fprintf(os.Stderr, "Passive URLs: %d\n", passiveCount)
	}
	fmt.Fprintf(os.Stderr, "Stop Reason: %s\n", stopReason)

	if deep || sourceMap || headless {
		deepCounters.Lock()
		fmt.Fprintf(os.Stderr, "\n--- Deep findings ---\n")
		fmt.Fprintf(os.Stderr, "XHR requests: %d\n", deepCounters.xhr)
		fmt.Fprintf(os.Stderr, "Forms found: %d\n", deepCounters.form)
		fmt.Fprintf(os.Stderr, "JSON body keys: %d\n", deepCounters.key)
		fmt.Fprintf(os.Stderr, "Custom headers: %d\n", deepCounters.header)
		fmt.Fprintf(os.Stderr, "Source maps: %d\n", deepCounters.sourcemap)
		deepCounters.Unlock()
	}

	if seg != nil {
		fmt.Fprintf(os.Stderr, "\n--- Segmented output ---\n")
		fmt.Fprintf(os.Stderr, "Directory: %s\n", outDir)
	}
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

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

func confirmYolo() bool {
	fmt.Fprint(os.Stderr, "\n  Continuar? [y/N] ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes" || line == "s"
}
