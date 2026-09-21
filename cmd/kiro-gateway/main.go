// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Command kiro-gateway es un proxy local que traduce las APIs de OpenAI y
// Anthropic a la API de Kiro. Port del ciclo de vida de .upstream/main.py:
// parseo de CLI (§7.3), config.Load (fase 2), wiring de las 7 package vars
// de converterscore (fase 3), construcción de httpclient/accountmanager
// (fase 4) y arranque de internal/server (Task 11, la última de la fase 5).
//
// # Desviaciones documentadas frente al brief/upstream
//
//  1. La SALIDA DE TERMINAL (ayuda, versión, banner de arranque, avisos,
//     errores y logs por-petición) está en inglés — lingua franca del
//     ecosistema — a diferencia del argparse en español que tuvo el port en
//     su primera fase. Los comentarios de código siguen en español (no son
//     UX). `--version` ya no imprime solo la cadena de versión: añade el
//     nombre del programa y una línea de paridad con el upstream
//     (`jwadow/kiro-gateway <Upstream> (commit <UpstreamCommit>)`); no hay
//     test que fije la salida exacta, y la mejora de UX prima sobre la
//     intención histórica de imprimir solo "2.4.dev.13+go".
//  2. --health apunta a 127.0.0.1 cuando el host resuelto es "0.0.0.0" o
//     vacío. SERVER_HOST=0.0.0.0 es una dirección de bind (todas las
//     interfaces), no un destino válido para un cliente HTTP saliente; el
//     propio flag --health es una adición del port ("adición" según el
//     spec §7.3, sin precedente en el argparse original) así que no hay
//     comportamiento upstream que replicar aquí. El banner de arranque hace
//     la misma conversión para mostrar una URL navegable (localhost).
//  3. LOG_LEVEL controla la verbosidad del logging operativo por-petición
//     (una línea slog por request a stdout). El upstream loguea con loguru;
//     el port lo cablea vía internal/server.logMiddleware. LOG_LEVEL=OFF lo
//     desactiva; el banner de arranque y los avisos NO se gatean por nivel.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/accountmanager"
	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/httpclient"
	"github.com/marr-cloud/kiro-gateway-go/internal/server"
	"github.com/marr-cloud/kiro-gateway-go/internal/version"
)

// shutdownTimeout es el plazo del cierre ordenado tras recibir SIGINT/SIGTERM
// (brief §"Graceful shutdown"): 30s.
const shutdownTimeout = 30 * time.Second

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run contiene toda la lógica de main como una función testeable: recibe los
// argumentos y los writers de salida (para que los tests capturen stdout/
// stderr) y devuelve el código de salida en vez de llamar a os.Exit.
func run(args []string, stdout, stderr io.Writer) int {
	var (
		host        string
		port        int
		showVersion bool
		showHelp    bool
		healthCheck bool
	)

	fs := flag.NewFlagSet("kiro-gateway", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&host, "host", "", "listen interface (default: SERVER_HOST or 0.0.0.0)")
	fs.StringVar(&host, "H", "", "shorthand for --host")
	fs.IntVar(&port, "port", 0, "listen port (default: SERVER_PORT or 8000)")
	fs.IntVar(&port, "p", 0, "shorthand for --port")
	fs.BoolVar(&showVersion, "version", false, "print version information and exit")
	fs.BoolVar(&showVersion, "v", false, "shorthand for --version")
	fs.BoolVar(&showHelp, "help", false, "print this help and exit")
	fs.BoolVar(&showHelp, "h", false, "shorthand for --help")
	fs.BoolVar(&healthCheck, "health", false, "query GET /health on a running server and exit 0 (healthy) or 1")

	// La ayuda automática (en error de parseo) va a stderr; --help explícito
	// va a stdout con exit 0.
	fs.Usage = func() { printUsage(stderr) }

	if err := fs.Parse(args); err != nil {
		return 2
	}

	switch {
	case showHelp:
		printUsage(stdout)
		return 0
	case showVersion:
		printVersion(stdout)
		return 0
	case healthCheck:
		return runHealthCheck(host, port, stdout, stderr)
	default:
		return runServer(host, port, stdout, stderr)
	}
}

// printUsage imprime la ayuda con un formato de CLI moderno: descripción,
// uso, flags agrupados (corto + largo en una sola línea) y ejemplos.
func printUsage(w io.Writer) {
	fmt.Fprint(w, `kiro-gateway — local proxy exposing OpenAI- and Anthropic-compatible APIs backed by Kiro.

Usage:
  kiro-gateway [flags]

Flags:
  -H, --host string   Listen interface (default: SERVER_HOST or 0.0.0.0)
  -p, --port int      Listen port (default: SERVER_PORT or 8000)
      --health        Query GET /health on a running server and exit 0 (healthy) or 1
  -v, --version       Print version information and exit
  -h, --help          Print this help and exit

Examples:
  kiro-gateway                Start on 0.0.0.0:8000
  kiro-gateway --port 9000    Start on port 9000
  kiro-gateway --health       Health-check a running server

Configuration is read from .env and credentials.json in the working directory.
Docs: https://github.com/marr-cloud/kiro-gateway-go
`)
}

// printVersion imprime el nombre del programa, su versión y la línea de
// paridad con el upstream portado (ver desviación 1 del comentario de cabecera).
func printVersion(w io.Writer) {
	fmt.Fprintf(w, "kiro-gateway %s\n", version.Version())
	fmt.Fprintf(w, "parity: jwadow/kiro-gateway %s (commit %s)\n", version.Upstream, version.UpstreamCommit)
}

// newLogger construye el logger operativo por-petición a partir de LOG_LEVEL
// (ya en mayúsculas por config). LOG_LEVEL=OFF (o NONE/SILENT) devuelve nil,
// que desactiva el logMiddleware; cualquier valor desconocido cae en INFO.
func newLogger(level string, out io.Writer) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "DEBUG":
		lvl = slog.LevelDebug
	case "WARN", "WARNING":
		lvl = slog.LevelWarn
	case "ERROR":
		lvl = slog.LevelError
	case "OFF", "NONE", "SILENT":
		return nil
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: lvl}))
}

// listenURL construye una URL navegable para el banner de arranque: un bind a
// 0.0.0.0 (o vacío) se muestra como localhost, que sí es un destino válido.
func listenURL(cfg *config.Config) string {
	host := cfg.ServerHost
	if host == "" || host == "0.0.0.0" {
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%d", host, cfg.ServerPort)
}

// runHealthCheck implementa `--health`: GET a /health del servidor que se
// asume ya en marcha en host:port, imprime el cuerpo y devuelve 0 si
// respondió 200, 1 en cualquier otro caso (spec §7.3).
func runHealthCheck(host string, port int, stdout, stderr io.Writer) int {
	cfg, err := config.Load(config.Options{Host: host, Port: port, DotenvPath: ".env"})
	if err != nil {
		fmt.Fprintln(stderr, "configuration error:", err)
		return 1
	}

	target := cfg.ServerHost
	if target == "" || target == "0.0.0.0" {
		// 0.0.0.0 es una dirección de bind, no de destino (ver desviación 2
		// del comentario de cabecera).
		target = "127.0.0.1"
	}

	url := fmt.Sprintf("http://%s:%d/health", target, cfg.ServerPort)
	resp, err := http.Get(url)
	if err != nil {
		fmt.Fprintln(stderr, "error querying /health:", err)
		return 1
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Fprintln(stdout, string(body))

	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// runServer implementa el ciclo de vida completo: config.Load → wire de las
// 7 package vars de converterscore → httpclient.New → accountmanager.NewManager
// → LoadCredentials → Initialize → SaveStatePeriodically (goroutine) →
// server.New → Start, con cierre ordenado ante SIGINT/SIGTERM.
func runServer(host string, port int, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(config.Options{Host: host, Port: port, DotenvPath: ".env"})
	if err != nil {
		fmt.Fprintln(stderr, "configuration error:", err)
		return 1
	}

	// Wire de las 7 package vars de converterscore (fase 3, config de
	// módulo mutable a propósito — ver task-11-brief.md).
	converterscore.FakeReasoningEnabled = cfg.FakeReasoning
	converterscore.FakeReasoningMaxTokens = cfg.FakeReasoningMaxTokens
	converterscore.FakeReasoningBudgetCap = cfg.FakeReasoningBudgetCap
	converterscore.TruncationRecoveryEnabled = cfg.TruncationRecovery
	converterscore.ToolDescriptionMaxLength = cfg.ToolDescriptionMaxLength
	converterscore.AutoTrimPayload = cfg.AutoTrimPayload
	converterscore.KiroMaxPayloadBytes = cfg.KiroMaxPayloadBytes

	// Logger operativo (LOG_LEVEL). Se instala también como default de slog
	// para que los reportes internos del debug logger (slog.Default) salgan
	// por el mismo canal.
	logger := newLogger(cfg.LogLevel, stdout)
	if logger != nil {
		slog.SetDefault(logger)
	}

	httpCli, err := httpclient.New(cfg)
	if err != nil {
		fmt.Fprintln(stderr, "failed to create HTTP client:", err)
		return 1
	}
	defer httpCli.Close()

	accounts, err := accountmanager.NewManager(cfg)
	if err != nil {
		fmt.Fprintln(stderr, "failed to create account manager:", err)
		return 1
	}

	if err := accounts.LoadCredentials(ctx); err != nil {
		fmt.Fprintln(stderr, "failed to load credentials:", err)
		return 1
	}

	if err := accounts.Initialize(ctx); err != nil {
		fmt.Fprintln(stderr, "failed to initialize accounts:", err)
		return 1
	}

	// Aviso accionable de primer arranque: sin cuentas el server arranca pero
	// cada petición devolverá 503. Se muestra siempre (no se gatea por nivel).
	if len(accounts.Accounts()) == 0 {
		fmt.Fprintln(stderr, "warning: no accounts loaded — create a credentials.json (see README); "+
			"requests will return 503 until an account is configured")
	}

	go accounts.SaveStatePeriodically(ctx)

	srv := server.New(cfg, accounts, httpCli, logger)

	// Cierre ordenado: al cancelarse ctx (SIGINT/SIGTERM), Shutdown corre en
	// su propio contexto con timeout de 30s — independiente de ctx, que ya
	// está cancelado en ese punto.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintln(stderr, "graceful shutdown error:", err)
		}
	}()

	fmt.Fprintf(stdout, "kiro-gateway %s listening on %s\n", version.Version(), listenURL(cfg))

	if err := srv.Start(ctx); err != nil {
		fmt.Fprintln(stderr, "server error:", err)
		return 1
	}
	return 0
}
