package crawler

import (
	"net/url"
	"regexp"
	"strings"

	"grau/internal/urlutil"
)

var (
	jsAPICallRegex = regexp.MustCompile(
		`(?i)\b(?:fetch|axios(?:\.(?:get|post|put|delete|patch|head|options|request))?|` +
			`\$\.(?:get|post|ajax|getJSON)|` +
			`new\s+(?:Worker|WebSocket|EventSource|SharedWorker)|` +
			`importScripts|` +
			`import)\s*\(\s*` +
			`(?:"([^"]{1,500})"|'([^']{1,500})'|` + "`([^`]{1,500})`" + `)`,
	)

	jsXHROpenRegex = regexp.MustCompile(
		`(?i)\.open\s*\(\s*["'](?:GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS)["']\s*,\s*` +
			`(?:"([^"]{1,500})"|'([^']{1,500})'|` + "`([^`]{1,500})`" + `)`,
	)

	jsKeyRegex = regexp.MustCompile(
		`(?i)\b(?:url|uri|src|href|endpoint|api|baseURL|base_url|baseUri|base_uri|` +
			`path|pathname|action|target|location|redirect|next|returnTo|return_url|` +
			`callback|webhook|host|origin|dest|destination|resource|asset|link|` +
			`apiUrl|api_url|gatewayApiUrl|gateway_api_url|serviceUrl|service_url|cdnUrl|cdn_url|cdnHost|cdn_host|` +
			`proxy|gateway|uploadURL|upload_url|downloadURL|download_url)` +
			`\b\s*[:=]\s*` +
			`(?:"([^"]{1,500})"|'([^']{1,500})'|` + "`([^`]{1,500})`" + `)`,
	)

	jsBarePathRegex = regexp.MustCompile(
		`"(/(?:[^"\\]|\\.){1,500})"` + `|` +
			`'(/(?:[^'\\]|\\.){1,500})'` + `|` +
			"`(/(?:[^`\\\\]|\\\\.){1,500})`" + `|` +
			`"(https?://(?:[^"\\]|\\.){1,500})"` + `|` +
			`'(https?://(?:[^'\\]|\\.){1,500})'` + `|` +
			"`(https?://(?:[^`\\\\]|\\\\.){1,500})`",
	)
)

var jsBlacklistExact = map[string]struct{}{
	"text/html": {}, "text/css": {}, "text/plain": {}, "text/javascript": {},
	"application/json": {}, "application/xml": {}, "application/javascript": {},
	"application/x-www-form-urlencoded": {}, "multipart/form-data": {},
	"image/png": {}, "image/jpeg": {}, "image/gif": {}, "image/svg+xml": {},
	"image/webp": {}, "image/avif": {},

	"GET": {}, "POST": {}, "PUT": {}, "DELETE": {}, "PATCH": {}, "HEAD": {}, "OPTIONS": {},

	"utf-8": {}, "utf8": {}, "ascii": {}, "latin1": {}, "utf-16": {},

	"true": {}, "false": {}, "null": {}, "undefined": {}, "NaN": {},

	"px": {}, "em": {}, "rem": {}, "vh": {}, "vw": {}, "%": {},
	"block": {}, "inline": {}, "flex": {}, "grid": {}, "none": {}, "auto": {},
	"GET ": {}, "POST ": {},

	"/": {}, "//": {}, "/.": {}, "/..": {},
}

var jsBlacklistPrefix = []string{
	"data:",
	"blob:",
	"javascript:",
	"mailto:",
	"tel:",
	"file:",
	"ftp:",
	"about:",
	"chrome:",
	"chrome-extension:",
	"moz-extension:",
	"ws://",
	"wss://",
}

// ExtractJSLinks extrai URLs/paths de um corpo JavaScript.
func ExtractJSLinks(body []byte, baseURL *url.URL, incomplete bool, loose bool) []string {
	if baseURL == nil || len(body) == 0 {
		return nil
	}
	s := string(body)
	seen := make(map[string]struct{}, 128)
	out := make([]string, 0, 128)

	add := func(raw string) {
		if n := resolveJS(raw, baseURL, incomplete); n != "" {
			// NOVO: rejeita URLs partidas
			if urlutil.LooksTruncated(n) {
				return
			}
			if _, dup := seen[n]; !dup {
				seen[n] = struct{}{}
				out = append(out, n)
			}
		}
	}

	for _, m := range jsAPICallRegex.FindAllStringSubmatch(s, -1) {
		add(firstNonEmpty(m[1], m[2], m[3]))
	}

	for _, m := range jsXHROpenRegex.FindAllStringSubmatch(s, -1) {
		add(firstNonEmpty(m[1], m[2], m[3]))
	}

	for _, m := range jsKeyRegex.FindAllStringSubmatch(s, -1) {
		add(firstNonEmpty(m[1], m[2], m[3]))
	}

	if loose {
		for _, m := range jsBarePathRegex.FindAllStringSubmatch(s, -1) {
			add(firstNonEmpty(m[1], m[2], m[3], m[4], m[5], m[6]))
		}
	}

	return out
}

func resolveJS(raw string, base *url.URL, incomplete bool) string {
	raw = strings.TrimSpace(raw)
	if !plausibleJSURL(raw) {
		return ""
	}

	rel, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	abs := base.ResolveReference(rel)
	return urlutil.Normalize(abs.String(), incomplete)
}

func plausibleJSURL(s string) bool {
	if len(s) < 2 || len(s) > 500 {
		return false
	}

	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7F:
			return false
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			return false
		case r >= 0x1F000:
			return false
		}
	}

	if strings.Contains(s, "${") {
		return false
	}
	if strings.Contains(s, "{{") || strings.Contains(s, "}}") {
		return false
	}
	if strings.Contains(s, "<%") || strings.Contains(s, "%>") {
		return false
	}

	if !strings.HasPrefix(s, "/") &&
		!strings.HasPrefix(s, "http://") &&
		!strings.HasPrefix(s, "https://") {
		return false
	}

	if s == "/" || s == "//" {
		return false
	}

	if strings.HasPrefix(s, "/") && strings.Contains(s[1:], "//") {
		return false
	}

	if _, bad := jsBlacklistExact[s]; bad {
		return false
	}
	lower := strings.ToLower(s)
	if _, bad := jsBlacklistExact[lower]; bad {
		return false
	}

	for _, p := range jsBlacklistPrefix {
		if strings.HasPrefix(lower, p) {
			return false
		}
	}

	if strings.ContainsAny(s, " \t\n\r") {
		return false
	}

	return true
}
