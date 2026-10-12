package crawler

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// XHRRequest é uma requisição XHR/fetch capturada pelo Chrome.
type XHRRequest struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Type   string `json:"type"`
	Body   string `json:"body,omitempty"`
}

// HeadlessResult é o que o Chrome devolve depois de renderizar a página.
type HeadlessResult struct {
	DOMLinks []string
	XHR      []XHRRequest
	Assets   []string
	FinalURL string
	Err      error
}

// FetchHeadless navega com Chrome headless e captura tudo.
func FetchHeadless(ctx context.Context, urlStr string, opt Options) HeadlessResult {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.UserAgent(userAgents[0]),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()

	tabCtx, cancelTab := chromedp.NewContext(allocCtx)
	defer cancelTab()

	tabCtx, cancelTimeout := context.WithTimeout(tabCtx, 30*time.Second)
	defer cancelTimeout()

	var (
		domLinks []string
		assets   []string
		xhr      []XHRRequest
		finalURL string
	)

	chromedp.ListenTarget(tabCtx, func(ev interface{}) {
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			req := e.Request
			rt := strings.ToLower(string(e.Type))
			switch rt {
			case "xhr", "fetch":
				xhr = append(xhr, XHRRequest{
					Method: req.Method,
					URL:    req.URL,
					Type:   rt,
				})
			case "script", "stylesheet", "image", "document":
				assets = append(assets, req.URL)
			}
		}
	})

	err := chromedp.Run(tabCtx,
		network.Enable(),
		chromedp.Navigate(urlStr),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return scrollToBottom(ctx, 2)
		}),
		chromedp.Location(&finalURL),
		chromedp.Evaluate(jsExtractLinks, &domLinks),
	)
	if err != nil {
		return HeadlessResult{Err: err}
	}

	return HeadlessResult{
		DOMLinks: domLinks,
		XHR:      xhr,
		Assets:   assets,
		FinalURL: finalURL,
	}
}

const jsExtractLinks = `(() => {
  const out = new Set();
  document.querySelectorAll('a[href]').forEach(a => out.add(a.href));
  document.querySelectorAll('script[src]').forEach(s => out.add(s.src));
  document.querySelectorAll('link[href]').forEach(l => out.add(l.href));
  document.querySelectorAll('iframe[src]').forEach(f => out.add(f.src));
  return Array.from(out);
})()`

func scrollToBottom(ctx context.Context, times int) error {
	for i := 0; i < times; i++ {
		if err := chromedp.Run(ctx,
			chromedp.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`, nil),
		); err != nil {
			return err
		}
		time.Sleep(400 * time.Millisecond)
	}
	return nil
}

// ChromeAvailable checa se há Chrome/Chromium instalado.
func ChromeAvailable() bool {
	paths := []string{
		"/usr/bin/google-chrome",
		"/usr/bin/chromium",
		"/usr/bin/chromium-browser",
		"/snap/bin/chromium",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
