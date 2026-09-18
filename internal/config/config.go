// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package config es la ÚNICA fuente de configuración derivada del entorno
// del gateway. Cada fase posterior lee su configuración exclusivamente de
// este paquete; ningún otro paquete llama a os.Getenv por su cuenta.
//
// Comportamiento (todo replicado a propósito para paridad con el upstream):
//
//  1. Lectura invertida del .env para KIRO_CREDS_FILE y KIRO_CLI_DB_FILE.
//     Estas dos variables se leen directamente del fichero .env sin procesar
//     escapes, así que una ruta Windows como C:\Users\x\creds.json sobrevive
//     literal. La variable de entorno actúa como fallback. Orden efectivo:
//     valor del .env → variable del shell → "".
//
//  2. Degradación silenciosa de enums. Un valor no reconocido para
//     DEBUG_MODE o FAKE_REASONING_HANDLING cae en el default sin log ni
//     aviso, porque el original tampoco avisa.
//
//  3. Regla booleana. Un booleano está activo si strings.ToLower(v) está
//     en {"true","1","yes"}. Cualquier otra cosa, incluida "" y valores
//     no reconocidos, desactiva.
//
//  4. FAKE_REASONING invertida (única de las 34). El modo está activo
//     SALVO que el valor esté en {"false","0","no","disabled","off"};
//     valores vacíos o ausentes lo activan. Verificado línea a línea contra
//     kiro/config.py.
//
// Precedencia unificada para toda variable normal:
//
//	Flag CLI (sólo Host y Port) >
//	shell (os.LookupEnv) >
//	fichero .env >
//	default de §7.2 del spec.
//
// Para las dos variables de ruta (KIRO_CREDS_FILE, KIRO_CLI_DB_FILE) el
// orden entre shell y .env se invierte, tal como marca config.py.
//
// Enteros y floats inválidos propagan error, replicando el upstream:
// float("potato") lanza ValueError en Python y el módulo config falla al
// importar. El port lo devuelve como error desde Load para que main pueda
// terminar con un mensaje claro en lugar de degradar en silencio.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Config carga cada campo desde una variable de entorno. El comentario a la
// derecha indica el nombre exacto de la variable; los campos van en el
// orden de la tabla §7.2 del spec para que la diferencia entre struct y
// tabla sea una línea por fila.
type Config struct {
	ProxyAPIKey                     string  // PROXY_API_KEY
	ServerHost                      string  // SERVER_HOST
	ServerPort                      int     // SERVER_PORT
	VPNProxyURL                     string  // VPN_PROXY_URL
	RefreshToken                    string  // REFRESH_TOKEN
	ProfileARN                      string  // PROFILE_ARN
	KiroRegion                      string  // KIRO_REGION
	KiroAPIRegion                   string  // KIRO_API_REGION (sin default; "" = sin fijar)
	KiroCredsFile                   string  // KIRO_CREDS_FILE (lectura cruda del .env)
	KiroCLIDBFile                   string  // KIRO_CLI_DB_FILE (lectura cruda del .env)
	SQLiteReadOnly                  bool    // SQLITE_READONLY
	AccountsConfigFile              string  // ACCOUNTS_CONFIG_FILE
	AccountsStateFile               string  // ACCOUNTS_STATE_FILE
	AccountRecoveryTimeout          int     // ACCOUNT_RECOVERY_TIMEOUT
	AccountMaxBackoffMultiplier     int     // ACCOUNT_MAX_BACKOFF_MULTIPLIER
	AccountProbabilisticRetryChance float64 // ACCOUNT_PROBABILISTIC_RETRY_CHANCE
	AccountCacheTTL                 int     // ACCOUNT_CACHE_TTL
	StateSaveIntervalSeconds        int     // STATE_SAVE_INTERVAL_SECONDS
	FirstTokenTimeout               float64 // FIRST_TOKEN_TIMEOUT
	FirstTokenMaxRetries            int     // FIRST_TOKEN_MAX_RETRIES
	StreamingReadTimeout            float64 // STREAMING_READ_TIMEOUT
	FakeReasoning                   bool    // FAKE_REASONING (lógica invertida)
	FakeReasoningMaxTokens          int     // FAKE_REASONING_MAX_TOKENS
	FakeReasoningBudgetCap          int     // FAKE_REASONING_BUDGET_CAP
	FakeReasoningHandling           string  // FAKE_REASONING_HANDLING (enum)
	FakeReasoningInitialBufferSize  int     // FAKE_REASONING_INITIAL_BUFFER_SIZE
	WebSearchEnabled                bool    // WEB_SEARCH_ENABLED
	AutoTrimPayload                 bool    // AUTO_TRIM_PAYLOAD
	KiroMaxPayloadBytes             int     // KIRO_MAX_PAYLOAD_BYTES
	ToolDescriptionMaxLength        int     // TOOL_DESCRIPTION_MAX_LENGTH
	TruncationRecovery              bool    // TRUNCATION_RECOVERY
	LogLevel                        string  // LOG_LEVEL (mayúsculas, sin validación)
	DebugMode                       string  // DEBUG_MODE (enum, silent fallback)
	DebugDir                        string  // DEBUG_DIR
}

// Options controla las entradas externas de Load. Host y Port vehiculan
// los overrides del flag CLI; DotenvPath indica qué fichero consultar como
// fallback tras el shell.
type Options struct {
	// Host sobrescribe SERVER_HOST cuando no está vacío. Se usa para
	// implementar la precedencia "flag CLI > entorno > default".
	Host string
	// Port sobrescribe SERVER_PORT cuando es distinto de cero. Cero
	// significa "sin override": el puerto real por defecto es 8000, así
	// que 0 nunca es un valor legítimo del CLI.
	Port int
	// DotenvPath es la ruta al fichero .env. Vacío significa "no leer
	// ningún .env": es lo que los tests suelen pasar para aislarse del
	// directorio de trabajo. El binario debe pasar explícitamente ".env".
	DotenvPath string
}

// Load resuelve las 34 variables siguiendo la precedencia documentada en
// el paquete. Devuelve un *Config completamente poblado, o un error si una
// variable entera o float tiene un valor no parseable, replicando la caída
// del import de config.py con ValueError.
func Load(opts Options) (*Config, error) {
	dotenv := parseDotenvFile(opts.DotenvPath)

	// lookup implementa la precedencia normal shell > .env > (unset).
	// Sirve para cada variable EXCEPTO las dos rutas, cuya lectura cruda
	// invierte el orden entre .env y shell.
	lookup := func(name string) (string, bool) {
		if v, ok := os.LookupEnv(name); ok {
			return v, true
		}
		if v, ok := dotenv[name]; ok {
			return v, true
		}
		return "", false
	}

	// lookupPath es la variante raw-first para las dos rutas. Sigue el
	// orden `_get_raw_env_value(...) or os.getenv(...)` de config.py.
	lookupPath := func(name string) (string, bool) {
		if v, ok := dotenv[name]; ok && v != "" {
			return v, true
		}
		if v, ok := os.LookupEnv(name); ok {
			return v, true
		}
		return "", false
	}

	// Se acumulan los errores de parseo de int/float para reportarlos
	// juntos. Multi-errores son raros, pero mostrarlos todos ahorra un
	// segundo arranque para descubrir el siguiente valor mal escrito.
	var errs []error
	getInt := func(name string, def int) int {
		raw, ok := lookup(name)
		v, err := parseInt(raw, ok, def)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
		return v
	}
	getFloat := func(name string, def float64) float64 {
		raw, ok := lookup(name)
		v, err := parseFloat(raw, ok, def)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
		return v
	}
	getString := func(name, def string) string {
		raw, ok := lookup(name)
		return parseString(raw, ok, def)
	}
	getBool := func(name string, def bool) bool {
		raw, ok := lookup(name)
		return parseBool(raw, ok, def)
	}
	getEnum := func(name string, valid []string, def string) string {
		raw, ok := lookup(name)
		return parseEnum(raw, ok, valid, def)
	}

	cfg := &Config{
		ProxyAPIKey:                     getString("PROXY_API_KEY", "my-super-secret-password-123"),
		ServerHost:                      getString("SERVER_HOST", "0.0.0.0"),
		ServerPort:                      getInt("SERVER_PORT", 8000),
		VPNProxyURL:                     getString("VPN_PROXY_URL", ""),
		RefreshToken:                    getString("REFRESH_TOKEN", ""),
		ProfileARN:                      getString("PROFILE_ARN", ""),
		KiroRegion:                      getString("KIRO_REGION", "us-east-1"),
		KiroAPIRegion:                   getString("KIRO_API_REGION", ""),
		SQLiteReadOnly:                  getBool("SQLITE_READONLY", false),
		AccountsConfigFile:              getString("ACCOUNTS_CONFIG_FILE", "credentials.json"),
		AccountsStateFile:               getString("ACCOUNTS_STATE_FILE", "state.json"),
		AccountRecoveryTimeout:          getInt("ACCOUNT_RECOVERY_TIMEOUT", 60),
		AccountMaxBackoffMultiplier:     getInt("ACCOUNT_MAX_BACKOFF_MULTIPLIER", 1440),
		AccountProbabilisticRetryChance: getFloat("ACCOUNT_PROBABILISTIC_RETRY_CHANCE", 0.1),
		AccountCacheTTL:                 getInt("ACCOUNT_CACHE_TTL", 43200),
		StateSaveIntervalSeconds:        getInt("STATE_SAVE_INTERVAL_SECONDS", 10),
		FirstTokenTimeout:               getFloat("FIRST_TOKEN_TIMEOUT", 15),
		FirstTokenMaxRetries:            getInt("FIRST_TOKEN_MAX_RETRIES", 3),
		StreamingReadTimeout:            getFloat("STREAMING_READ_TIMEOUT", 300),
		FakeReasoningMaxTokens:          getInt("FAKE_REASONING_MAX_TOKENS", 4000),
		FakeReasoningBudgetCap:          getInt("FAKE_REASONING_BUDGET_CAP", 10000),
		FakeReasoningHandling: getEnum("FAKE_REASONING_HANDLING",
			[]string{"as_reasoning_content", "remove", "pass", "strip_tags"},
			"as_reasoning_content"),
		FakeReasoningInitialBufferSize: getInt("FAKE_REASONING_INITIAL_BUFFER_SIZE", 20),
		WebSearchEnabled:               getBool("WEB_SEARCH_ENABLED", true),
		AutoTrimPayload:                getBool("AUTO_TRIM_PAYLOAD", false),
		KiroMaxPayloadBytes:            getInt("KIRO_MAX_PAYLOAD_BYTES", 600000),
		ToolDescriptionMaxLength:       getInt("TOOL_DESCRIPTION_MAX_LENGTH", 10000),
		TruncationRecovery:             getBool("TRUNCATION_RECOVERY", true),
		DebugMode:                      getEnum("DEBUG_MODE", []string{"off", "errors", "all"}, "off"),
		DebugDir:                       getString("DEBUG_DIR", "debug_logs"),
	}

	// LOG_LEVEL: sin validación pero pasado por .upper(). Un LOG_LEVEL
	// vacío queda "" en el original y aquí también; los consumidores lo
	// interpretan como "no elegido".
	rawLog, okLog := lookup("LOG_LEVEL")
	cfg.LogLevel = strings.ToUpper(parseString(rawLog, okLog, "INFO"))

	// FAKE_REASONING con lógica invertida. lookup nos daría (raw,isSet)
	// pero parseBoolInverted no necesita distinguir "unset" de "empty":
	// ambos casos activan el modo por defecto.
	rawFR, _ := lookup("FAKE_REASONING")
	cfg.FakeReasoning = parseBoolInverted(rawFR)

	// Rutas: lectura cruda del .env con fallback al shell. No pasan por
	// filepath.Clean para no arriesgar transformaciones espurias en
	// Windows; os.Open acepta ambos separadores sin problema.
	rawCreds, _ := lookupPath("KIRO_CREDS_FILE")
	cfg.KiroCredsFile = rawCreds
	rawCLIDB, _ := lookupPath("KIRO_CLI_DB_FILE")
	cfg.KiroCLIDBFile = rawCLIDB

	// Los overrides del CLI se aplican al final para que ganen a todo.
	if opts.Host != "" {
		cfg.ServerHost = opts.Host
	}
	if opts.Port != 0 {
		cfg.ServerPort = opts.Port
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return cfg, nil
}
