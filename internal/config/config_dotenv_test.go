// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

func TestCLIOverridesEnvironment(t *testing.T) {
	// Precedencia: CLI > entorno > default. Se comprueba en las dos
	// direcciones para evitar un test que pase por accidente.
	isolateEnv(t)
	t.Setenv("SERVER_HOST", "10.0.0.1")
	t.Setenv("SERVER_PORT", "9001")

	cfg, err := config.Load(config.Options{Host: "127.0.0.1", Port: 9999})
	if err != nil {
		t.Fatalf("Load con overrides: %v", err)
	}
	if cfg.ServerHost != "127.0.0.1" {
		t.Errorf("con override, ServerHost = %q, quiero 127.0.0.1", cfg.ServerHost)
	}
	if cfg.ServerPort != 9999 {
		t.Errorf("con override, ServerPort = %d, quiero 9999", cfg.ServerPort)
	}

	// Sin overrides, gana el entorno.
	cfg, err = config.Load(config.Options{})
	if err != nil {
		t.Fatalf("Load sin overrides: %v", err)
	}
	if cfg.ServerHost != "10.0.0.1" {
		t.Errorf("sin override, ServerHost = %q, quiero 10.0.0.1", cfg.ServerHost)
	}
	if cfg.ServerPort != 9001 {
		t.Errorf("sin override, ServerPort = %d, quiero 9001", cfg.ServerPort)
	}
}

func TestEnvOverridesDefault(t *testing.T) {
	// Comprueba que la variable de entorno gana al default para todos los
	// tipos primitivos: string, int, float, enum válido.
	isolateEnv(t)
	t.Setenv("PROXY_API_KEY", "nuevo-secreto")
	t.Setenv("SERVER_PORT", "12345")
	t.Setenv("FIRST_TOKEN_TIMEOUT", "42.5")
	t.Setenv("DEBUG_MODE", "all")

	cfg, err := config.Load(config.Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ProxyAPIKey != "nuevo-secreto" {
		t.Errorf("ProxyAPIKey = %q", cfg.ProxyAPIKey)
	}
	if cfg.ServerPort != 12345 {
		t.Errorf("ServerPort = %d", cfg.ServerPort)
	}
	if cfg.FirstTokenTimeout != 42.5 {
		t.Errorf("FirstTokenTimeout = %v", cfg.FirstTokenTimeout)
	}
	if cfg.DebugMode != "all" {
		t.Errorf("DebugMode = %q", cfg.DebugMode)
	}
}

func TestShellEnvBeatsDotenv(t *testing.T) {
	// Para variables que NO son los dos paths, la shell gana al .env
	// (matches load_dotenv que no sobreescribe).
	isolateEnv(t)
	t.Setenv("PROXY_API_KEY", "de-la-shell")
	dotenv := writeDotenv(t, "PROXY_API_KEY=del-fichero\n")
	cfg, err := config.Load(config.Options{DotenvPath: dotenv})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ProxyAPIKey != "de-la-shell" {
		t.Errorf("ProxyAPIKey = %q, quiero de-la-shell", cfg.ProxyAPIKey)
	}
}

func TestDotenvIsFallbackForUnsetShell(t *testing.T) {
	isolateEnv(t)
	dotenv := writeDotenv(t, "SERVER_HOST=1.2.3.4\nSERVER_PORT=7777\n")
	cfg, err := config.Load(config.Options{DotenvPath: dotenv})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ServerHost != "1.2.3.4" {
		t.Errorf("ServerHost = %q, quiero 1.2.3.4 (del .env)", cfg.ServerHost)
	}
	if cfg.ServerPort != 7777 {
		t.Errorf("ServerPort = %d, quiero 7777 (del .env)", cfg.ServerPort)
	}
}

func TestInvalidIntPropagatesError(t *testing.T) {
	// La fuente (config.py) llama a int(os.getenv(...)) sin manejo de error,
	// así que un valor inválido aborta la importación del módulo. Se replica
	// devolviendo error desde Load: main() lo verá y saldrá con un mensaje
	// claro en lugar de un default silencioso que enmascare el problema.
	isolateEnv(t)
	t.Setenv("SERVER_PORT", "potato")
	if _, err := config.Load(config.Options{}); err == nil {
		t.Fatal("quiero error con SERVER_PORT=potato, obtuve nil")
	}
}

func TestInvalidFloatPropagatesError(t *testing.T) {
	isolateEnv(t)
	t.Setenv("FIRST_TOKEN_TIMEOUT", "no-un-numero")
	if _, err := config.Load(config.Options{}); err == nil {
		t.Fatal("quiero error con FIRST_TOKEN_TIMEOUT=no-un-numero, obtuve nil")
	}
}

func TestLogLevelIsUppercased(t *testing.T) {
	// LOG_LEVEL se pasa por .upper() en el original: cualquier caja llega
	// mayúscula al resto del sistema. Sin validación: "potato" → "POTATO".
	isolateEnv(t)
	t.Setenv("LOG_LEVEL", "debug")
	cfg, err := config.Load(config.Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != "DEBUG" {
		t.Errorf("LogLevel = %q, quiero DEBUG", cfg.LogLevel)
	}

	// Sin validación de valores permitidos: se conserva el original.
	isolateEnv(t)
	t.Setenv("LOG_LEVEL", "potato")
	cfg, err = config.Load(config.Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != "POTATO" {
		t.Errorf("LogLevel = %q, quiero POTATO (LOG_LEVEL no valida)", cfg.LogLevel)
	}
}

func TestDotenvQuotedValues(t *testing.T) {
	// Las comillas dobles y simples se recortan; los backslashes de la ruta
	// no se interpretan como escapes.
	isolateEnv(t)
	dotenv := writeDotenv(t, strings.Join([]string{
		`PROXY_API_KEY="con espacios"`,
		`ACCOUNTS_CONFIG_FILE='C:\Users\x\creds.json'`,
		`# comentario`,
		``,
		`REFRESH_TOKEN=sin-comillas`,
	}, "\n"))
	cfg, err := config.Load(config.Options{DotenvPath: dotenv})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ProxyAPIKey != "con espacios" {
		t.Errorf("ProxyAPIKey = %q", cfg.ProxyAPIKey)
	}
	if cfg.AccountsConfigFile != `C:\Users\x\creds.json` {
		t.Errorf("AccountsConfigFile = %q", cfg.AccountsConfigFile)
	}
	if cfg.RefreshToken != "sin-comillas" {
		t.Errorf("RefreshToken = %q", cfg.RefreshToken)
	}
}

func TestDotenvMissingFileIsSilent(t *testing.T) {
	// Ni fichero ni error: Load simplemente usa entorno o defaults.
	isolateEnv(t)
	cfg, err := config.Load(config.Options{DotenvPath: filepath.Join(t.TempDir(), "no-existe.env")})
	if err != nil {
		t.Fatalf("Load con .env inexistente: %v", err)
	}
	if cfg.ServerPort != 8000 {
		t.Errorf("ServerPort = %d, quiero 8000 (default)", cfg.ServerPort)
	}
}
