package output

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// SegmentedWriter escreve em vários arquivos, um por categoria.
//
// Uso típico:
//
//	sw, _ := NewSegmented("./out")
//	defer sw.Close()
//	sw.WriteURL("https://x/y")
//	sw.WriteParam("https://x/y?id=1")
//	sw.WriteJS("https://x/app.js")
//	sw.WriteForm("https://x/login", `{"action":...}`)
//	sw.WriteKey("email")
//	sw.WriteHeader("X-Api-Key")
//	sw.WriteSourceMap("https://x/app.js.map")
type SegmentedWriter struct {
	dir string
	mu  sync.Mutex

	files map[string]*segFile
}

type segFile struct {
	f   *os.File
	buf *bufio.Writer
}

var segNames = map[string]string{
	"urls":       "urls.txt",
	"params":     "params.txt",
	"js":         "js.txt",
	"xhr":        "xhr.json",
	"forms":      "forms.json",
	"keys":       "keys.txt",
	"headers":    "headers.txt",
	"sourcemaps": "sourcemaps.txt",
}

// NewSegmented abre o diretório e prepara os arquivos sob demanda.
func NewSegmented(dir string) (*SegmentedWriter, error) {
	if dir == "" {
		return nil, fmt.Errorf("out-dir vazio")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &SegmentedWriter{
		dir:   dir,
		files: map[string]*segFile{},
	}, nil
}

// Path devolve o caminho absoluto de um arquivo segmentado.
func (s *SegmentedWriter) Path(kind string) string {
	name, ok := segNames[kind]
	if !ok {
		name = kind + ".txt"
	}
	return filepath.Join(s.dir, name)
}

// writeLine é o núcleo: abre o arquivo sob demanda e escreve uma linha.
func (s *SegmentedWriter) writeLine(kind, line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, ok := s.files[kind]
	if !ok {
		name, exists := segNames[kind]
		if !exists {
			name = kind + ".txt"
		}
		f, err := os.OpenFile(filepath.Join(s.dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		sf = &segFile{f: f, buf: bufio.NewWriterSize(f, 64*1024)}
		s.files[kind] = sf
	}
	_, err := sf.buf.WriteString(line + "\n")
	return err
}

// --- Métodos por categoria ---

func (s *SegmentedWriter) WriteURL(u string) error        { return s.writeLine("urls", u) }
func (s *SegmentedWriter) WriteParam(u string) error      { return s.writeLine("params", u) }
func (s *SegmentedWriter) WriteJS(u string) error         { return s.writeLine("js", u) }
func (s *SegmentedWriter) WriteXHR(jsonLine string) error { return s.writeLine("xhr", jsonLine) }
func (s *SegmentedWriter) WriteForm(url, jsonLine string) error {
	return s.writeLine("forms", jsonLine)
}
func (s *SegmentedWriter) WriteKey(k string) error       { return s.writeLine("keys", k) }
func (s *SegmentedWriter) WriteHeader(h string) error    { return s.writeLine("headers", h) }
func (s *SegmentedWriter) WriteSourceMap(u string) error { return s.writeLine("sourcemaps", u) }

// Flush esvazia todos os buffers.
func (s *SegmentedWriter) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sf := range s.files {
		_ = sf.buf.Flush()
	}
}

// Close faz flush e fecha tudo.
func (s *SegmentedWriter) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var lastErr error
	for _, sf := range s.files {
		_ = sf.buf.Flush()
		if err := sf.f.Close(); err != nil {
			lastErr = err
		}
	}
	s.files = map[string]*segFile{}
	return lastErr
}
