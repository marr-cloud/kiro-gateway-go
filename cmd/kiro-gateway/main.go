// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Command kiro-gateway es un proxy local que traduce las APIs de OpenAI y
// Anthropic a la API de Kiro.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/marr-cloud/kiro-gateway-go/internal/version"
)

func main() {
	var (
		host        string
		port        int
		showVersion bool
	)

	fs := flag.NewFlagSet("kiro-gateway", flag.ExitOnError)
	fs.StringVar(&host, "host", "", "interfaz de escucha (por defecto SERVER_HOST o 0.0.0.0)")
	fs.StringVar(&host, "H", "", "abreviatura de --host")
	fs.IntVar(&port, "port", 0, "puerto de escucha (por defecto SERVER_PORT o 8000)")
	fs.IntVar(&port, "p", 0, "abreviatura de --port")
	fs.BoolVar(&showVersion, "version", false, "imprime la versión y sale")
	fs.BoolVar(&showVersion, "v", false, "abreviatura de --version")

	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if showVersion {
		fmt.Printf("Kiro Gateway %s\n", version.Version())
		return
	}

	// La fase 5 sustituye esto por el arranque del servidor.
	fmt.Fprintln(os.Stderr, "el servidor todavía no está implementado: ver la fase 5 del port")
	os.Exit(1)
}
