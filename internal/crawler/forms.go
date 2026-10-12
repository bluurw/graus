package crawler

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// FormFields descreve o que um <form> envia.
type FormFields struct {
	Action string      `json:"action"`
	Method string      `json:"method"`
	Fields []FormField `json:"fields"`
}

// FormField é um campo individual.
type FormField struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
}

// JSON serializa o form para guardar em DeepFinding.Data.
func (f FormFields) JSON() string {
	b, _ := json.Marshal(f)
	return string(b)
}

// ExtractFormsFromHTML percorre o HTML e devolve todos os <form>.
func ExtractFormsFromHTML(body []byte, contentType string, baseURL *url.URL) []FormFields {
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return nil
	}
	doc, err := html.Parse(r)
	if err != nil {
		return nil
	}

	var forms []FormFields
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			f := FormFields{Method: "GET"}
			for _, a := range n.Attr {
				switch a.Key {
				case "action":
					if baseURL != nil {
						if u, err := url.Parse(a.Val); err == nil {
							f.Action = baseURL.ResolveReference(u).String()
						} else {
							f.Action = a.Val
						}
					} else {
						f.Action = a.Val
					}
				case "method":
					f.Method = strings.ToUpper(a.Val)
				}
			}
			var inner func(*html.Node)
			inner = func(c *html.Node) {
				if c.Type == html.ElementNode {
					switch c.Data {
					case "input", "textarea", "select":
						var ff FormField
						for _, a := range c.Attr {
							switch a.Key {
							case "name":
								ff.Name = a.Val
							case "type":
								ff.Type = a.Val
							case "value":
								ff.Value = a.Val
							}
						}
						if ff.Name != "" && ff.Type != "submit" && ff.Type != "button" {
							f.Fields = append(f.Fields, ff)
						}
					}
				}
				for k := c.FirstChild; k != nil; k = k.NextSibling {
					inner(k)
				}
			}
			inner(n)
			if f.Action != "" && len(f.Fields) > 0 {
				forms = append(forms, f)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return forms
}

// jsonBodyRegex acha objetos JSON literais dentro de JS.
var jsonBodyRegex = regexp.MustCompile(
	`(?s)JSON\.stringify\s*\(\s*\{([^{}]{1,1000})\}`,
)

// ExtractJSONBodyKeys extrai as chaves de objetos passados em JSON.stringify.
func ExtractJSONBodyKeys(js string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, m := range jsonBodyRegex.FindAllStringSubmatch(js, -1) {
		for _, pair := range strings.Split(m[1], ",") {
			kv := strings.SplitN(pair, ":", 2)
			if len(kv) != 2 {
				continue
			}
			k := strings.Trim(strings.TrimSpace(kv[0]), `"'`)
			if k == "" {
				continue
			}
			if _, dup := seen[k]; !dup {
				seen[k] = struct{}{}
				out = append(out, k)
			}
		}
	}
	return out
}

// headerRegex acha headers customizados referenciados em JS.
var headerRegex = regexp.MustCompile(
	`["'](X-[A-Za-z0-9-]+|Authorization|Cookie|X-Api-Key)["']\s*:`,
)

// ExtractCustomHeaders devolve headers customizados citados no JS.
func ExtractCustomHeaders(js string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, m := range headerRegex.FindAllStringSubmatch(js, -1) {
		if _, dup := seen[m[1]]; !dup {
			seen[m[1]] = struct{}{}
			out = append(out, m[1])
		}
	}
	return out
}
