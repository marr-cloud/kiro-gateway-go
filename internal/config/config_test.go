// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

// allEnvVars lista las 34 variables que Load lee. Se conserva aquí para que
// el aislamiento sea exhaustivo: si se añade un campo al struct sin registrar
// su variable, cualquier test que dependa de defaults empezará a fallar en
// una máquina que la tenga puesta en el entorno.
var allEnvVars = []string{
	"PROXY_API_KEY", "SERVER_HOST", "SERVER_PORT", "VPN_PROXY_URL",
	"REFRESH_TOKEN", "PROFILE_ARN", "KIRO_REGION", "KIRO_API_REGION",
	"KIRO_CREDS_FILE", "KIRO_CLI_DB_FILE", "SQLITE_READONLY",
	"ACCOUNTS_CONFIG_FILE", "ACCOUNTS_STATE_FILE", "ACCOUNT_RECOVERY_TIMEOUT",
	"ACCOUNT_MAX_BACKOFF_MULTIPLIER", "ACCOUNT_PROBABILISTIC_RETRY_CHANCE",
	"ACCOUNT_CACHE_TTL", "STATE_SAVE_INTERVAL_SECONDS", "FIRST_TOKEN_TIMEOUT",
	"FIRST_TOKEN_MAX_RETRIES", "STREAMING_READ_TIMEOUT", "FAKE_REASONING",
	"FAKE_REASONING_MAX_TOKENS", "FAKE_REASONING_BUDGET_CAP",
	"FAKE_REASONING_HANDLING", "FAKE_REASONING_INITIAL_BUFFER_SIZE",
	"WEB_SEARCH_ENABLED", "AUTO_TRIM_PAYLOAD", "KIRO_MAX_PAYLOAD_BYTES",
	"TOOL_DESCRIPTION_MAX_LENGTH", "TRUNCATION_RECOVERY", "LOG_LEVEL",
	"DEBUG_MODE", "DEBUG_DIR",
}

// isolateEnv borra del entorno las 34 variables que Load lee y programa su
// restauración al terminar el test. Se llama al principio de cada test que
// inspecciona el resultado de Load para que la shell del usuario no pueda
// influir en el resultado. testing.T no expone Unsetenv, así que se registra
// una limpieza manual.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, k := range allEnvVars {
		k := k
		orig, had := os.LookupEnv(k)
		os.Unsetenv(k)
		if had {
			t.Cleanup(func() { _ = os.Setenv(k, orig) })
		}
	}
}

// writeDotenv escribe contents en un fichero .env dentro de t.TempDir() y
// devuelve la ruta. testing.T se encarga de borrarlo al terminar el test.
func writeDotenv(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("escribiendo .env temporal: %v", err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	isolateEnv(t)
	cfg, err := config.Load(config.Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Tabla con las 34 variables en el mismo orden que §7.2, para que un
	// revisor pueda comparar fila a fila sin recalcular.
	type row struct {
		name string
		got  any
		want any
	}
	rows := []row{
		{"PROXY_API_KEY", cfg.ProxyAPIKey, "my-super-secret-password-123"},
		{"SERVER_HOST", cfg.ServerHost, "0.0.0.0"},
		{"SERVER_PORT", cfg.ServerPort, 8000},
		{"VPN_PROXY_URL", cfg.VPNProxyURL, ""},
		{"REFRESH_TOKEN", cfg.RefreshToken, ""},
		{"PROFILE_ARN", cfg.ProfileARN, ""},
		{"KIRO_REGION", cfg.KiroRegion, "us-east-1"},
		{"KIRO_API_REGION", cfg.KiroAPIRegion, ""},
		{"KIRO_CREDS_FILE", cfg.KiroCredsFile, ""},
		{"KIRO_CLI_DB_FILE", cfg.KiroCLIDBFile, ""},
		{"SQLITE_READONLY", cfg.SQLiteReadOnly, false},
		{"ACCOUNTS_CONFIG_FILE", cfg.AccountsConfigFile, "credentials.json"},
		{"ACCOUNTS_STATE_FILE", cfg.AccountsStateFile, "state.json"},
		{"ACCOUNT_RECOVERY_TIMEOUT", cfg.AccountRecoveryTimeout, 60},
		{"ACCOUNT_MAX_BACKOFF_MULTIPLIER", cfg.AccountMaxBackoffMultiplier, 1440},
		{"ACCOUNT_PROBABILISTIC_RETRY_CHANCE", cfg.AccountProbabilisticRetryChance, 0.1},
		{"ACCOUNT_CACHE_TTL", cfg.AccountCacheTTL, 43200},
		{"STATE_SAVE_INTERVAL_SECONDS", cfg.StateSaveIntervalSeconds, 10},
		{"FIRST_TOKEN_TIMEOUT", cfg.FirstTokenTimeout, 15.0},
		{"FIRST_TOKEN_MAX_RETRIES", cfg.FirstTokenMaxRetries, 3},
		{"STREAMING_READ_TIMEOUT", cfg.StreamingReadTimeout, 300.0},
		{"FAKE_REASONING", cfg.FakeReasoning, true},
		{"FAKE_REASONING_MAX_TOKENS", cfg.FakeReasoningMaxTokens, 4000},
		{"FAKE_REASONING_BUDGET_CAP", cfg.FakeReasoningBudgetCap, 10000},
		{"FAKE_REASONING_HANDLING", cfg.FakeReasoningHandling, "as_reasoning_content"},
		{"FAKE_REASONING_INITIAL_BUFFER_SIZE", cfg.FakeReasoningInitialBufferSize, 20},
		{"WEB_SEARCH_ENABLED", cfg.WebSearchEnabled, true},
		{"AUTO_TRIM_PAYLOAD", cfg.AutoTrimPayload, false},
		{"KIRO_MAX_PAYLOAD_BYTES", cfg.KiroMaxPayloadBytes, 600000},
		{"TOOL_DESCRIPTION_MAX_LENGTH", cfg.ToolDescriptionMaxLength, 10000},
		{"TRUNCATION_RECOVERY", cfg.TruncationRecovery, true},
		{"LOG_LEVEL", cfg.LogLevel, "INFO"},
		{"DEBUG_MODE", cfg.DebugMode, "off"},
		{"DEBUG_DIR", cfg.DebugDir, "debug_logs"},
	}
	if len(rows) != 34 {
		t.Fatalf("la tabla de defaults tiene %d filas, quiero 34", len(rows))
	}
	for _, r := range rows {
		if r.got != r.want {
			t.Errorf("%s = %#v (%T), quiero %#v (%T)", r.name, r.got, r.got, r.want, r.want)
		}
	}
}

func TestFakeReasoningIsInverted(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
		want  bool
	}{
		{"unset_activa", "", false, true},
		{"vacio_activa", "", true, true},
		{"potato_activa", "potato", true, true},
		{"true_activa", "true", true, true},
		{"false_desactiva", "false", true, false},
		{"cero_desactiva", "0", true, false},
		{"no_desactiva", "no", true, false},
		{"disabled_desactiva", "disabled", true, false},
		{"off_desactiva", "off", true, false},
		{"OFF_desactiva_mayusculas", "OFF", true, false},
		{"False_desactiva_mixto", "False", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateEnv(t)
			if c.set {
				t.Setenv("FAKE_REASONING", c.value)
			}
			cfg, err := config.Load(config.Options{})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.FakeReasoning != c.want {
				t.Errorf("FAKE_REASONING=%q set=%t → FakeReasoning=%v, quiero %v",
					c.value, c.set, cfg.FakeReasoning, c.want)
			}
		})
	}
}

func TestBooleanParsing(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"1", true},
		{"yes", true},
		{"TRUE", true},
		{"True", true},
		{"YES", true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"potato", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run("SQLITE_READONLY="+c.value, func(t *testing.T) {
			isolateEnv(t)
			t.Setenv("SQLITE_READONLY", c.value)
			cfg, err := config.Load(config.Options{})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.SQLiteReadOnly != c.want {
				t.Errorf("SQLITE_READONLY=%q → %v, quiero %v", c.value, cfg.SQLiteReadOnly, c.want)
			}
		})
	}
}

func TestEnumsFallBackSilently(t *testing.T) {
	debugCases := []struct{ in, want string }{
		{"off", "off"},
		{"errors", "errors"},
		{"all", "all"},
		{"ALL", "all"},
		{"potato", "off"},
		{"", "off"},
	}
	for _, c := range debugCases {
		t.Run("DEBUG_MODE="+c.in, func(t *testing.T) {
			isolateEnv(t)
			t.Setenv("DEBUG_MODE", c.in)
			cfg, err := config.Load(config.Options{})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.DebugMode != c.want {
				t.Errorf("DEBUG_MODE=%q → %q, quiero %q", c.in, cfg.DebugMode, c.want)
			}
		})
	}

	handlingCases := []struct{ in, want string }{
		{"as_reasoning_content", "as_reasoning_content"},
		{"remove", "remove"},
		{"pass", "pass"},
		{"strip_tags", "strip_tags"},
		{"AS_REASONING_CONTENT", "as_reasoning_content"},
		{"xyz", "as_reasoning_content"},
		{"", "as_reasoning_content"},
	}
	for _, c := range handlingCases {
		t.Run("FAKE_REASONING_HANDLING="+c.in, func(t *testing.T) {
			isolateEnv(t)
			t.Setenv("FAKE_REASONING_HANDLING", c.in)
			cfg, err := config.Load(config.Options{})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.FakeReasoningHandling != c.want {
				t.Errorf("FAKE_REASONING_HANDLING=%q → %q, quiero %q",
					c.in, cfg.FakeReasoningHandling, c.want)
			}
		})
	}
}

func TestCredsPathFromDotenvIsNotUnescaped(t *testing.T) {
	// Este es el test que protege a los usuarios de Windows: la ruta
	// C:\Users\x\creds.json contiene la secuencia \U que un parser con
	// interpretación de escapes convertiría en un carácter Unicode.
	const winPath = `C:\Users\x\creds.json`

	t.Run("KIRO_CREDS_FILE", func(t *testing.T) {
		isolateEnv(t)
		dotenv := writeDotenv(t, "KIRO_CREDS_FILE="+winPath+"\n")
		cfg, err := config.Load(config.Options{DotenvPath: dotenv})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.KiroCredsFile != winPath {
			t.Errorf("KiroCredsFile = %q, quiero %q", cfg.KiroCredsFile, winPath)
		}
	})

	t.Run("KIRO_CLI_DB_FILE", func(t *testing.T) {
		isolateEnv(t)
		dotenv := writeDotenv(t, "KIRO_CLI_DB_FILE="+winPath+"\n")
		cfg, err := config.Load(config.Options{DotenvPath: dotenv})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.KiroCLIDBFile != winPath {
			t.Errorf("KiroCLIDBFile = %q, quiero %q", cfg.KiroCLIDBFile, winPath)
		}
	})

	t.Run("dotenv_gana_sobre_env_para_estas_dos", func(t *testing.T) {
		// Quirk documentado: para KIRO_CREDS_FILE/KIRO_CLI_DB_FILE la
		// lectura cruda del .env va primero, así que el .env gana a la
		// variable de entorno del shell aunque ambas estén puestas. Es lo
		// que hace config.py con `_get_raw_env_value(...) or os.getenv(...)`.
		isolateEnv(t)
		t.Setenv("KIRO_CREDS_FILE", "/from/shell/creds.json")
		dotenv := writeDotenv(t, "KIRO_CREDS_FILE="+winPath+"\n")
		cfg, err := config.Load(config.Options{DotenvPath: dotenv})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.KiroCredsFile != winPath {
			t.Errorf("KiroCredsFile = %q, quiero %q (el .env gana)", cfg.KiroCredsFile, winPath)
		}
	})

	t.Run("shell_es_fallback_si_no_esta_en_dotenv", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("KIRO_CREDS_FILE", "/from/shell/creds.json")
		dotenv := writeDotenv(t, "OTHER=irrelevant\n")
		cfg, err := config.Load(config.Options{DotenvPath: dotenv})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.KiroCredsFile != "/from/shell/creds.json" {
			t.Errorf("KiroCredsFile = %q, quiero /from/shell/creds.json", cfg.KiroCredsFile)
		}
	})
}
