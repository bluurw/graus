package crawler

import (
	"net/url"
	"regexp"
	"strings"

	"grau/internal/urlutil"
)

// ---------------------------------------------------------------------------
// Regex por camada (compiladas uma vez)
// ---------------------------------------------------------------------------

var (
	// Camada 1 — APIs com URL garantida:
	//   fetch("...")         fetch('...')         fetch(`...`)
	//   axios.get("...")     axios.post('...')    axios.request(...)
	//   $.get(...)           $.post(...)          $.ajax(...)
	//   $.getJSON(...)
	//   XMLHttpRequest.open(...)  (pega o 2º arg? não — pegamos o 1º que é método GET/POST... ver abaixo)
	//   new Worker("...")    new WebSocket("...")  new EventSource("...")
	//   importScripts("...")
	//   import("...")
	jsAPICallRegex = regexp.MustCompile(
		`(?i)\b(?:fetch|axios(?:\.(?:get|post|put|delete|patch|head|options|request))?|` +
			`\$\.(?:get|post|ajax|getJSON)|` +
			`new\s+(?:Worker|WebSocket|EventSource|SharedWorker)|` +
			`importScripts|` +
			`import)\s*\(\s*` +
			`(?:"([^"]{1,500})"|'([^']{1,500})'|` + "`([^`]{1,500})`" + `)`,
	)

	// Camada 1b — XMLHttpRequest.open(method, url):
	// O 2º argumento é a URL. Casamos explicitamente o método antes.
	jsXHROpenRegex = regexp.MustCompile(
		`(?i)\.open\s*\(\s*["'](?:GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS)["']\s*,\s*` +
			`(?:"([^"]{1,500})"|'([^']{1,500})'|` + "`([^`]{1,500})`" + `)`,
	)

	// Camada 2 — chaves/atribuições com nomes que sugerem URL.
	//   url: "..."    src: "..."    baseURL: "..."    endpoint: "..."    path: "..."
	//   url = "..."   (também cobre assignment)
	jsKeyRegex = regexp.MustCompile(
		`(?i)\b(?:url|uri|src|href|endpoint|api|baseURL|base_url|baseUri|base_uri|` +
			`path|pathname|action|target|location|redirect|next|returnTo|return_url|` +
			`callback|webhook|host|origin|dest|destination|resource|asset|link|` +
			`proxy|gateway|uploadURL|upload_url|downloadURL|download_url)` +
			`\b\s*[:=]\s*` +
			`(?:"([^"]{1,500})"|'([^']{1,500})'|` + "`([^`]{1,500})`" + `)`,
	)

	// Camada 3 — strings soltas que começam com "/" ou "http(s)://"
	//   "/api/v1/users"     '/admin/panel'     `https://cdn.x/y`
	//
	// Aqui aceitamos aspas duplas, simples e template literals.
	jsBarePathRegex = regexp.MustCompile(
		`"(/(?:[^"\\]|\\.){1,500})"` + `|` +
			`'(/(?:[^'\\]|\\.){1,500})'` + `|` +
			"`(/(?:[^`\\\\]|\\\\.){1,500})`" + `|` +
			`"(https?://(?:[^"\\]|\\.){1,500})"` + `|` +
			`'(https?://(?:[^'\\]|\\.){1,500})'` + `|` +
			"`(https?://(?:[^`\\\\]|\\\\.){1,500})`",
	)
)

// ---------------------------------------------------------------------------
// Blacklist — o que NUNCA é URL
// ---------------------------------------------------------------------------

var jsBlacklistExact = map[string]struct{}{
	// MIME types
	"text/html": {}, "text/css": {}, "text/plain": {}, "text/javascript": {},
	"application/json": {}, "application/xml": {}, "application/javascript": {},
	"application/x-www-form-urlencoded": {}, "multipart/form-data": {},
	"image/png": {}, "image/jpeg": {}, "image/gif": {}, "image/svg+xml": {},
	"image/webp": {}, "image/avif": {},

	// Métodos HTTP
	"GET": {}, "POST": {}, "PUT": {}, "DELETE": {}, "PATCH": {}, "HEAD": {}, "OPTIONS": {},

	// Encoding / direções
	"utf-8": {}, "utf8": {}, "ascii": {}, "latin1": {}, "utf-16": {},

	// Literais
	"true": {}, "false": {}, "null": {}, "undefined": {}, "NaN": {},

	// Unidades / keywords comuns em strings
	"px": {}, "em": {}, "rem": {}, "vh": {}, "vw": {}, "%": {},
	"block": {}, "inline": {}, "flex": {}, "grid": {}, "none": {}, "auto": {},
	"GET ": {}, "POST ": {},

	// Paths triviais
	"/": {}, "//": {}, "/.": {}, "/..": {},
}

// jsBlacklistPrefix — prefixos que indicam protocolo não-HTTP.
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
	"wss://", // WebSocket puro — não é HTTP
}

// ---------------------------------------------------------------------------
// API pública
// ---------------------------------------------------------------------------

// ExtractJSLinks extrai URLs/paths de um corpo JavaScript.
//
// Estratégia em camadas:
//   - Camada 1  — fetch/axios/XHR/Worker/import   (alta precisão)
//   - Camada 2  — chaves url/src/baseURL/path     (média precisão)
//   - Camada 3  — strings soltas começando em "/" ou "http"  (baixa precisão)
//
// loose=true libera a camada 3. loose=false só roda 1 e 2.
//
// Retorna URLs normalizadas, sem duplicatas, na ordem de aparição.
func ExtractJSLinks(body []byte, baseURL *url.URL, incomplete bool, loose bool) []string {
	if baseURL == nil || len(body) == 0 {
		return nil
	}
	s := string(body)
	seen := make(map[string]struct{}, 128)
	out := make([]string, 0, 128)

	add := func(raw string) {
		if n := resolveJS(raw, baseURL, incomplete); n != "" {
			if _, dup := seen[n]; !dup {
				seen[n] = struct{}{}
				out = append(out, n)
			}
		}
	}

	// Camada 1 — chamadas de API com URL garantida
	for _, m := range jsAPICallRegex.FindAllStringSubmatch(s, -1) {
		add(firstNonEmpty(m[1], m[2], m[3]))
	}

	// Camada 1b — XMLHttpRequest.open("GET", "url")
	for _, m := range jsXHROpenRegex.FindAllStringSubmatch(s, -1) {
		add(firstNonEmpty(m[1], m[2], m[3]))
	}

	// Camada 2 — chaves sensíveis
	for _, m := range jsKeyRegex.FindAllStringSubmatch(s, -1) {
		add(firstNonEmpty(m[1], m[2], m[3]))
	}

	// Camada 3 — strings soltas (só se loose)
	if loose {
		for _, m := range jsBarePathRegex.FindAllStringSubmatch(s, -1) {
			add(firstNonEmpty(m[1], m[2], m[3], m[4], m[5], m[6]))
		}
	}

	return out
}

// ---------------------------------------------------------------------------
// Validação e resolução
// ---------------------------------------------------------------------------

// resolveJS valida, resolve contra a base e normaliza.
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

// plausibleJSURL é o filtro anti-falso-positivo.
//
// Regras (todas têm que passar):
//   - Tamanho entre 2 e 500
//   - Sem caracteres de controle, espaço, tab, newline
//   - Sem emojis (>= U+1F000)
//   - Não contém "${" (template literal não resolvido)
//   - Não contém "{{" nem "}}" (placeholders)
//   - Começa com "/" ou "http://" ou "https://"
//   - Não é apenas "/" ou "//"
//   - Não tem "//" no meio (fora do scheme)
//   - Não bate na blacklist exata nem de prefixo
func plausibleJSURL(s string) bool {
	if len(s) < 2 || len(s) > 500 {
		return false
	}

	// Caracteres inválidos
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7F:
			return false
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			return false
		case r >= 0x1F000: // emoji
			return false
		}
	}

	// Template literal não resolvido
	if strings.Contains(s, "${") {
		return false
	}
	if strings.Contains(s, "{{") || strings.Contains(s, "}}") {
		return false
	}
	if strings.Contains(s, "<%") || strings.Contains(s, "%>") {
		return false
	}

	// Precisa começar bem
	if !strings.HasPrefix(s, "/") &&
		!strings.HasPrefix(s, "http://") &&
		!strings.HasPrefix(s, "https://") {
		return false
	}

	// Triviais
	if s == "/" || s == "//" {
		return false
	}

	// "//" no meio de um path — provavelmente comentário ou concatenção
	if strings.HasPrefix(s, "/") && strings.Contains(s[1:], "//") {
		return false
	}

	// Blacklist exata
	if _, bad := jsBlacklistExact[s]; bad {
		return false
	}
	lower := strings.ToLower(s)
	if _, bad := jsBlacklistExact[lower]; bad {
		return false
	}

	// Blacklist de prefixo
	for _, p := range jsBlacklistPrefix {
		if strings.HasPrefix(lower, p) {
			return false
		}
	}

	// Whitespace internalizado (já coberto por r==' ' acima, mas defesa em profundidade)
	if strings.ContainsAny(s, " \t\n\r") {
		return false
	}

	return true
}
