// Package terminal fornece utilitários de formatação e cores para o CLI.
package terminal

import (
	"fmt"
	"time"
)

// Códigos ANSI
const (
	Reset   = "\033[0m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	Gray    = "\033[90m"
	Bold    = "\033[1m"
)

// ColorEnabled controla se as cores serão emitidas. Definido em main().
var ColorEnabled = false

// Color envolve uma string com a cor ANSI se as cores estiverem habilitadas.
func Color(c, s string) string {
	if !ColorEnabled {
		return s
	}
	return c + s + Reset
}

// Art imprime a arte ASCII do grau.
func Art() {
	art := `
 ▄█▀████████▄███ ▄█▀██▄ ▀███  ▀███  
▄██  ██   ██▀ ▀▀██   ██   ██    ██  
▀█████▀   ██     ▄█████   ██    ██  
██        ██    ██   ██   ██    ██  
 ███████▄████▄  ▀████▀██▄ ▀████▀███▄
█▀   ██ by: github.com/bluurw               
██████▀         ~grau v10~
`
	fmt.Println(art)
}

// Now retorna o timestamp formatado para logs.
func Now() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// Duration formata uma duração de forma compacta (1d2h3m4s).
func Duration(d time.Duration) string {
	d = d.Round(time.Second)
	days := d / (24 * time.Hour)
	h := (d % (24 * time.Hour)) / time.Hour
	m := (d % time.Hour) / time.Minute
	s := (d % time.Minute) / time.Second
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh%dm%ds", days, h, m, s)
	case h > 0:
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm%ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// StatusColor retorna (string, cor) para um código HTTP.
func StatusColor(status int) (string, string) {
	switch {
	case status == 0:
		return "ERROR", Red
	case status >= 500:
		return fmt.Sprintf("%d", status), Red
	case status >= 400:
		return fmt.Sprintf("%d", status), Yellow
	case status >= 300:
		return fmt.Sprintf("%d", status), Magenta
	case status >= 200:
		return fmt.Sprintf("%d", status), Green
	case status >= 100:
		return fmt.Sprintf("%d", status), Gray
	default:
		return fmt.Sprintf("%d", status), Gray
	}
}
