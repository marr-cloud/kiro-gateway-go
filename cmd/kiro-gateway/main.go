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
//  1. --version imprime SOLO version.Version() (p.ej. "2.4.dev.13+go"), sin
//     el prefijo "Kiro Gateway " que llevaba el stub de esta fase y sin el
//     "%(prog)s " que antepone argparse en el original (main.py:641-644:
//     version=f"%(prog)s {APP_VERSION}", así que `python main.py -v`
//     imprime "main.py 2.4.dev.13"). El criterio de cierre de esta tarea
//     exige literalmente que `--version` imprima "2.4.dev.13+go" — imprimir
//     solo esa cadena es la interpretación más segura frente a un chequeo
//     exacto de stdout.
//  2. --health apunta a 127.0.0.1 cuando el host resuelto es "0.0.0.0" o
//     vacío. SERVER_HOST=0.0.0.0 es una dirección de bind (todas las
//     interfaces), no un destino válido para un cliente HTTP saliente; el
//     propio flag --health es una adición del port ("adición" según el
//     spec §7.3, sin precedente en el argparse original) así que no hay
//     comportamiento upstream que replicar aquí.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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
	os.Exit(run(os.Args[1:]))
}

// run contiene toda la lógica de main como una función testeable que
// devuelve el código de salida en vez de llamar a os.Exit directamente.
func run(args []string) int {
	var (
		host        string
		port        int
		showVersion bool
		showHelp    bool
		healthCheck bool
	)

	fs := flag.NewFlagSet("kiro-gateway", flag.ContinueOnError)
	fs.StringVar(&host, "host", "", "interfaz de escucha (por defecto SERVER_HOST o 0.0.0.0)")
	fs.StringVar(&host, "H", "", "abreviatura de --host")
	fs.IntVar(&port, "port", 0, "puerto de escucha (por defecto SERVER_PORT o 8000)")
	fs.IntVar(&port, "p", 0, "abreviatura de --port")
	fs.BoolVar(&showVersion, "version", false, "imprime la versión y sale")
	fs.BoolVar(&showVersion, "v", false, "abreviatura de --version")
	fs.BoolVar(&showHelp, "help", false, "imprime esta ayuda y sale")
	fs.BoolVar(&showHelp, "h", false, "abreviatura de --help")
	fs.BoolVar(&healthCheck, "health", false, "consulta GET /health del servidor en marcha y sale con 0 (sano) o 1")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Uso: kiro-gateway [flags]\n\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return 2
	}

	switch {
	case showHelp:
		fs.Usage()
		return 0
	case showVersion:
		fmt.Println(version.Version())
		return 0
	case healthCheck:
		return runHealthCheck(host, port)
	default:
		return runServer(host, port)
	}
}

// runHealthCheck implementa `--health`: GET a /health del servidor que se
// asume ya en marcha en host:port, imprime el cuerpo y devuelve 0 si
// respondió 200, 1 en cualquier otro caso (spec §7.3).
func runHealthCheck(host string, port int) int {
	cfg, err := config.Load(config.Options{Host: host, Port: port, DotenvPath: ".env"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error de configuración:", err)
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
		fmt.Fprintln(os.Stderr, "error consultando /health:", err)
		return 1
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Println(string(body))

	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// runServer implementa el ciclo de vida completo: config.Load → wire de las
// 7 package vars de converterscore → httpclient.New → accountmanager.NewManager
// → LoadCredentials → Initialize → SaveStatePeriodically (goroutine) →
// server.New → Start, con cierre ordenado ante SIGINT/SIGTERM.
func runServer(host string, port int) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(config.Options{Host: host, Port: port, DotenvPath: ".env"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error de configuración:", err)
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

	httpCli, err := httpclient.New(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error creando el cliente HTTP:", err)
		return 1
	}
	defer httpCli.Close()

	accounts, err := accountmanager.NewManager(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error creando el gestor de cuentas:", err)
		return 1
	}

	if err := accounts.LoadCredentials(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error cargando credenciales:", err)
		return 1
	}

	if err := accounts.Initialize(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error inicializando cuentas:", err)
		return 1
	}

	go accounts.SaveStatePeriodically(ctx)

	srv := server.New(cfg, accounts, httpCli)

	// Cierre ordenado: al cancelarse ctx (SIGINT/SIGTERM), Shutdown corre en
	// su propio contexto con timeout de 30s — independiente de ctx, que ya
	// está cancelado en ese punto.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintln(os.Stderr, "error durante el cierre ordenado:", err)
		}
	}()

	fmt.Printf("Kiro Gateway %s escuchando en %s:%d\n", version.Version(), cfg.ServerHost, cfg.ServerPort)

	if err := srv.Start(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error del servidor:", err)
		return 1
	}
	return 0
}
