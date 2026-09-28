// Package output centraliza a escrita em stdout e no arquivo de saída.
package output

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// Writer escreve URLs em stdout e, opcionalmente, em arquivo.
//
// Dois modos:
//   - Text:  uma URL por linha (padrão)
//   - JSON:  uma linha JSON por URL com metadados
type Writer struct {
	f       *os.File
	buf     *bufio.Writer
	enabled bool

	Quiet bool // não escreve em stdout
	JSON  bool // modo JSON Lines
}

// New abre o arquivo de saída (se path != "").
func New(path string) (*Writer, error) {
	w := &Writer{}
	if path == "" {
		return w, nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	w.f = f
	w.buf = bufio.NewWriterSize(f, 64*1024)
	w.enabled = true
	return w, nil
}

// Record é o formato JSON emitido por linha.
type Record struct {
	URL    string `json:"url"`
	Status int    `json:"status,omitempty"`
	CT     string `json:"ct,omitempty"`
	RTms   int64  `json:"rt_ms,omitempty"`
	Source string `json:"source,omitempty"`
}

// WriteLine imprime em stdout (se não Quiet) e escreve no arquivo.
func (w *Writer) WriteLine(s string) {
	if !w.Quiet {
		fmt.Println(s)
	}
	if w.enabled {
		_, _ = w.buf.WriteString(s)
		_ = w.buf.WriteByte('\n')
	}
}

// WriteRecord escreve um registro (texto ou JSON, conforme modo).
func (w *Writer) WriteRecord(r Record) {
	if w.JSON {
		b, _ := json.Marshal(r)
		line := string(b)
		if !w.Quiet {
			fmt.Println(line)
		}
		if w.enabled {
			_, _ = w.buf.WriteString(line)
			_ = w.buf.WriteByte('\n')
		}
		return
	}
	w.WriteLine(r.URL)
}

// Flush garante que buffers sejam esvaziados.
func (w *Writer) Flush() {
	if w.buf != nil {
		_ = w.buf.Flush()
	}
}

// Close faz flush e fecha o arquivo.
func (w *Writer) Close() error {
	if w.f == nil {
		return nil
	}
	w.Flush()
	return w.f.Close()
}
