// Package output centraliza a escrita em stdout e no arquivo de saída.
package output

import (
	"bufio"
	"fmt"
	"os"
)

// Writer escreve URLs em stdout e, opcionalmente, em arquivo.
type Writer struct {
	f       *os.File
	buf     *bufio.Writer
	enabled bool
}

// New abre o arquivo de saída (se path != ""). Erros são reportados no retorno.
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

// WriteLine imprime em stdout e escreve no arquivo (se habilitado).
func (w *Writer) WriteLine(s string) {
	fmt.Println(s)
	if w.enabled {
		_, _ = w.buf.WriteString(s)
		_ = w.buf.WriteByte('\n')
	}
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
