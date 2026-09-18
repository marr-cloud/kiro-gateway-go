# Fase 6b — Simplificar credenciales a solo `credentials.json` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hacer de `credentials.json` (nombrado por `ACCOUNTS_CONFIG_FILE`) la única fuente de cuentas del port, retirando los knobs de config muertos/engañosos (`ACCOUNT_SYSTEM`) y las variables de credenciales de cuenta única (`KIRO_CREDS_FILE`, `KIRO_CLI_DB_FILE`) del entorno global, dejando el `.env` para `PROXY_API_KEY` + servidor/comportamiento.

**Architecture:** Tres cambios en secuencia. (1) `internal/config` deja de parsear del entorno los vars retirados y lee las rutas de cuentas (`ACCOUNTS_CONFIG_FILE`/`ACCOUNTS_STATE_FILE`) con la precedencia raw-first (.env gana) que antes tenían las rutas de credenciales. (2) `internal/accountmanager` cablea el descubrimiento y el `state.json` por defecto a `AccountsConfigFile`, y se repuntan todos los helpers de test que apuntaban el descubrimiento vía `KiroCredsFile`. (3) Documentación (README ES/EN, `credentials.json.example`, `DIFFERENCES.md`, tabla de config del spec).

**Tech Stack:** Go 1.27, biblioteca estándar, `internal/config`, `internal/accountmanager`, `internal/auth` (sin cambios; sigue leyendo los campos por-cuenta).

**Spec:** `docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md`. Este plan **enmienda** deliberadamente el modelo de credenciales del spec (§6.1, §6.11 y la tabla de config §777-781): el port ya había dejado sin implementar el modo de cuenta única (`internal/server/server.go:26-28`), y este plan termina esa simplificación de forma coherente. El modelo de descubrimiento (§6.11: array con `type` json/sqlite/refresh_token, `enabled`, validación) no cambia.

## Global Constraints

- Go 1.27, `CGO_ENABLED=0`, **sin nuevas dependencias externas**.
- Cada fichero `.go` nuevo empieza con la cabecera SPDX de 2 líneas:
  `// SPDX-License-Identifier: AGPL-3.0-or-later` / `// Port a Go de jwadow/kiro-gateway. Ver NOTICE.`
- Todo fichero (incluidos tests) < 400 líneas.
- **Sin nuevos globales de paquete mutables.**
- `os.Getenv` solo en `internal/config`.
- Este cambio **no toca ninguna frontera de red** (§D1): el corpus golden no debe verse afectado. La verificación es `go build ./... && go vet ./... && go test ./... -count=1` en verde.
- Comentarios y mensajes en español (convención del repo).

**Hecho verificado (no re-descubrir):**
- `discovery.go:35` lee `m.cfg.KiroCredsFile` como el array `credentials.json` (el único consumidor global de ese campo, junto con el default de state en `manager.go:64-75`).
- `auth` lee `cfg.KiroCredsFile`/`cfg.KiroCLIDBFile` **por cuenta** (`auth/manager.go:173,187`), rellenados por `discovery.processFile`/`processRefreshTokenEntry`. **Los campos del struct se conservan.**
- En producción `auth` se construye solo vía `auth.NewManagerForAccount` (per-cuenta, desde `discovery`); `auth.NewManager(cfg)` global solo se usa en tests. Retirar el parseo global de `KIRO_CREDS_FILE`/`KIRO_CLI_DB_FILE` es seguro.
- `config.go`: `AccountSystem`/`AccountsConfigFile`/`AccountsStateFile` se parsean en el literal (líneas 186-188); `KIRO_CREDS_FILE`/`KIRO_CLI_DB_FILE` se leen raw con `lookupPath` (líneas 227-230). `lookupPath` = `.env` no vacío > shell > unset. `lookup` = shell > `.env` > unset.

---

### Task 1: `internal/config` — retirar knobs muertos y leer rutas de cuentas raw-first

**Files:**
- Modify: `internal/config/config.go` (struct + `Load`)
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Consume: `lookupPath(name) (string, bool)` (raw-first), `parseString(raw, ok, def) string` (ya existentes en el paquete).
- Produce: `Config` sin el campo `AccountSystem`; `Config.AccountsConfigFile`/`AccountsStateFile` poblados con precedencia raw-first; `Config.KiroCredsFile`/`KiroCLIDBFile` con valor **cero** (`""`) por defecto (ya no se leen del entorno global; siguen existiendo como campos y los rellena `discovery` por cuenta).

- [ ] **Step 1: Ajustar los tests de config primero (rojo esperado)**

En `internal/config/config_test.go`:
1. Quitar la fila de la tabla de defaults que comprueba `ACCOUNT_SYSTEM`:
   `{"ACCOUNT_SYSTEM", cfg.AccountSystem, false},` (config_test.go:88) — el campo se elimina.
2. Quitar las dos filas que comprueban el parseo global de las rutas de credenciales de cuenta única:
   `{"KIRO_CREDS_FILE", cfg.KiroCredsFile, ""},` y `{"KIRO_CLI_DB_FILE", cfg.KiroCLIDBFile, ""},` (config_test.go:85-86) — ya no se leen del entorno.
3. Reemplazar los sub-tests que verifican la lectura raw de `KIRO_CREDS_FILE`/`KIRO_CLI_DB_FILE` desde el `.env` (config_test.go:~253-296, "el .env gana", ruta Windows) por el **mismo contrato sobre `ACCOUNTS_CONFIG_FILE`**: una ruta Windows con backslashes puesta en el `.env` sobrevive literal y gana al shell. Ejemplo del test a dejar (adaptar los helpers `writeDotenv`/`t.Setenv` ya presentes en el fichero):

```go
t.Run("ACCOUNTS_CONFIG_FILE_raw_env_wins_over_shell", func(t *testing.T) {
	winPath := `C:\Users\x\credentials.json`
	dotenv := writeDotenv(t, "ACCOUNTS_CONFIG_FILE="+winPath+"\n")
	t.Setenv("ACCOUNTS_CONFIG_FILE", "/from/shell/credentials.json")
	cfg, err := Load(Options{DotenvPath: dotenv})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AccountsConfigFile != winPath {
		t.Errorf("AccountsConfigFile = %q, quiero %q (el .env gana, raw)", cfg.AccountsConfigFile, winPath)
	}
})
```
Conservar el test existente que ya comprueba una ruta Windows en `ACCOUNTS_CONFIG_FILE` (config_test.go:452-453) — debe seguir en verde.
4. Si `config_test.go` lista los nombres de variables en un slice (config_test.go:22), quitar de él `ACCOUNT_SYSTEM` si su presencia fuerza una comprobación del campo eliminado (dejar `KIRO_CREDS_FILE`/`KIRO_CLI_DB_FILE` solo si el test restante no asume su parseo del entorno).

- [ ] **Step 2: Ejecutar los tests de config (rojo)**

Run: `go test ./internal/config/ -count=1`
Expected: FAIL de compilación (`cfg.AccountSystem` no existe) tras el Step 3, o de aserción hasta implementar. (Este paso confirma que los tests ejercen el cambio.)

- [ ] **Step 3: Implementar en `internal/config/config.go`**

1. Eliminar el campo del struct `AccountSystem bool // ACCOUNT_SYSTEM` (config.go:68).
2. En el literal de `cfg := &Config{...}`, eliminar las líneas:
   - `AccountSystem: getBool("ACCOUNT_SYSTEM", false),` (186)
   - `AccountsConfigFile: getString("ACCOUNTS_CONFIG_FILE", "credentials.json"),` (187)
   - `AccountsStateFile: getString("ACCOUNTS_STATE_FILE", "state.json"),` (188)
3. Reemplazar el bloque de lectura raw de rutas (config.go:224-230) por la lectura raw-first de las rutas de **cuentas** (las de credenciales de cuenta única quedan sin leer del entorno):

```go
	// Rutas de cuentas: lectura raw-first (.env gana sobre el shell) para
	// preservar backslashes de Windows, la misma precedencia que antes tenían
	// las rutas de credenciales de cuenta única (ya retiradas: el port solo
	// carga cuentas desde ACCOUNTS_CONFIG_FILE, ver DIFFERENCES.md). No pasan
	// por filepath.Clean: os.Open acepta ambos separadores.
	rawAccts, okAccts := lookupPath("ACCOUNTS_CONFIG_FILE")
	cfg.AccountsConfigFile = parseString(rawAccts, okAccts, "credentials.json")
	rawState, okState := lookupPath("ACCOUNTS_STATE_FILE")
	cfg.AccountsStateFile = parseString(rawState, okState, "state.json")
```

4. Los campos `KiroCredsFile`/`KiroCLIDBFile` del struct **se conservan** (los usa `auth` por cuenta), pero ya no se pueblan desde el entorno: quedan en `""` por defecto. Actualizar sus comentarios de campo para reflejar "fijado por cuenta desde cada entrada de credentials.json; no se lee del entorno".

- [ ] **Step 4: Ejecutar los tests de config (verde)**

Run: `go test ./internal/config/ -count=1`
Expected: PASS.

- [ ] **Step 5: Build + vet del módulo (detectar referencias muertas a `AccountSystem`)**

Run: `go build ./... && go vet ./...`
Expected: limpio. Si algo fuera de tests referenciaba `cfg.AccountSystem`, saldría aquí (verificado: no hay código de producción que lo haga).

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "refactor(config): retirar ACCOUNT_SYSTEM y las rutas de cuenta unica; leer ACCOUNTS_CONFIG_FILE raw-first"
```

---

### Task 2: `internal/accountmanager` — descubrir desde `AccountsConfigFile` + repuntar helpers de test

**Files:**
- Modify: `internal/accountmanager/discovery.go:34-35`
- Modify: `internal/accountmanager/manager.go:64-75`
- Modify (helpers de test que apuntan el DESCUBRIMIENTO vía `KiroCredsFile`): `internal/accountmanager/manager_test.go`, `internal/accountmanager/integration_test.go`, `internal/routesopenai/helpers_test.go`, `internal/routesanthropic/helpers_test.go`, `internal/server/server_test.go`
- Add: un test en `internal/accountmanager/manager_test.go` que verifique que `LoadCredentials` lee de `AccountsConfigFile`.

**Interfaces:**
- Consume: `m.cfg.AccountsConfigFile` (poblado en Task 1).
- Produce: descubrimiento de cuentas keyed en `AccountsConfigFile`; sin cambios de firma pública.

**Regla de repunteo (crítica, clasificar cada sitio):** cambiar `KiroCredsFile:`/`cfg.KiroCredsFile =` → `AccountsConfigFile:`/`cfg.AccountsConfigFile =` **solo** donde ese valor sea la ruta a un **array `credentials.json`** consumido por `LoadCredentials`/`discovery`. **NO** tocar los sitios donde `KiroCredsFile`/`KiroCLIDBFile` es la ruta de una credencial **por cuenta** consumida por `auth` (todo `internal/auth/*_test.go`, y `internal/accountmanager/init_test.go` cuando construye `auth.NewManagerForAccount`/`auth.NewManager` con un token JSON individual). En la duda, el discriminador es: ¿el fichero apuntado es un **array** de entradas (`[{...}]`) o un **token JSON/SQLite individual**? El array → `AccountsConfigFile`; el individual → se queda.

- [ ] **Step 1: Escribir el test de que `LoadCredentials` lee de `AccountsConfigFile`**

Añadir a `internal/accountmanager/manager_test.go` (reusar el patrón existente que escribe un `credentials.json` temporal y llama a `LoadCredentials`, pero fijando `AccountsConfigFile` en vez de `KiroCredsFile`):

```go
// TestLoadCredentials_ReadsFromAccountsConfigFile verifica que el descubrimiento
// de cuentas se dirige por AccountsConfigFile (ACCOUNTS_CONFIG_FILE), no por el
// retirado KiroCredsFile.
func TestLoadCredentials_ReadsFromAccountsConfigFile(t *testing.T) {
	dir := t.TempDir()
	credsPath := filepath.Join(dir, "credentials.json")
	// Una entrada refresh_token es autosuficiente (no necesita fichero externo).
	if err := os.WriteFile(credsPath, []byte(`[{"type":"refresh_token","enabled":true,"refresh_token":"tok-123"}]`), 0o600); err != nil {
		t.Fatalf("write creds: %v", err)
	}
	cfg := baseTestConfig(t) // helper existente; ajustar si el nombre difiere
	cfg.AccountsConfigFile = credsPath
	cfg.KiroCredsFile = "" // asegurar que el descubrimiento NO depende del var retirado

	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.LoadCredentials(context.Background()); err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if got := len(m.Accounts()); got != 1 {
		t.Fatalf("cuentas cargadas = %d, quiero 1", got)
	}
}
```
(Si no existe un `baseTestConfig`, construir el `*config.Config` mínimo inline como hacen los tests vecinos.)

- [ ] **Step 2: Ejecutar el test nuevo (rojo)**

Run: `go test ./internal/accountmanager/ -run TestLoadCredentials_ReadsFromAccountsConfigFile -count=1 -v`
Expected: FAIL — `discovery` aún lee `KiroCredsFile` (vacío) → 0 cuentas.

- [ ] **Step 3: Cablear el descubrimiento y el default de state**

1. `internal/accountmanager/discovery.go:35`: `credsFilePath := m.cfg.KiroCredsFile` → `credsFilePath := m.cfg.AccountsConfigFile`. Ajustar el comentario si nombra la variable.
2. `internal/accountmanager/manager.go:67`: en el fallback del state file, `filepath.Dir(cfg.KiroCredsFile)` → `filepath.Dir(cfg.AccountsConfigFile)`, y la guarda `if cfg.KiroCredsFile != ""` → `if cfg.AccountsConfigFile != ""`.

- [ ] **Step 4: Repuntar los helpers de test del descubrimiento**

Aplicar la **Regla de repunteo** a: `manager_test.go` (líneas ~70,183,238,305,356,418,483,532,593 — las que fijan un array `credentials.json`), `integration_test.go:56`, `routesopenai/helpers_test.go:86`, `routesanthropic/helpers_test.go:73`, `server/server_test.go:68` (ver también la nota en :108). Cambiar `KiroCredsFile` → `AccountsConfigFile` solo en esos sitios de array. Leer cada helper para confirmar que el fichero apuntado es un array antes de cambiarlo.

- [ ] **Step 5: Ejecutar el test nuevo (verde) y las suites afectadas**

Run: `go test ./internal/accountmanager/ ./internal/routesopenai/ ./internal/routesanthropic/ ./internal/server/ -count=1`
Expected: PASS. Si algún test de `auth` o `init_test` se rompió, es que se repuntó un sitio **por cuenta** por error: revertir ese sitio (regla del Step 4).

- [ ] **Step 6: Build + vet + suite completa**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: todo PASS (el corpus golden incluido: este cambio no toca fronteras de red).

- [ ] **Step 7: Commit**

```bash
git add internal/accountmanager/ internal/routesopenai/helpers_test.go internal/routesanthropic/helpers_test.go internal/server/server_test.go
git commit -m "refactor(accountmanager): descubrir cuentas desde ACCOUNTS_CONFIG_FILE en vez de KIRO_CREDS_FILE"
```

---

### Task 3: Documentación — README ES/EN, ejemplo, DIFFERENCES y spec

**Files:**
- Modify: `README.md`, `README.en.md` (sección Configuración)
- Create: `credentials.json.example`
- Modify: `docs/DIFFERENCES.md`
- Modify: `docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md` (tabla de config §777-781 y notas §6.1/§6.11)

**Interfaces:** ninguna (solo prosa + un fichero de ejemplo).

- [ ] **Step 1: `credentials.json.example`**

Crear en la raíz un array comentado (JSON no admite comentarios, así que van en el README; el ejemplo lleva las tres formas):

```json
[
  { "type": "json", "enabled": true, "path": "C:/Users/tu-usuario/.aws/sso/cache/kiro-auth-token.json" },
  { "type": "sqlite", "enabled": true, "path": "C:/Users/tu-usuario/AppData/Local/kiro-cli/data.sqlite3" },
  { "type": "refresh_token", "enabled": false, "refresh_token": "eyJ...", "profile_arn": "arn:aws:codewhisperer:us-east-1:..." }
]
```

- [ ] **Step 2: README ES/EN — reescribir la sección Configuración**

Sustituir las "Opciones 1/2/3" heredadas del upstream (REFRESH_TOKEN suelto, KIRO_CREDS_FILE→token JSON, KIRO_CLI_DB_FILE suelto) por el modelo real:
- Las **cuentas** se definen en un array `credentials.json` (por defecto en el cwd; o `ACCOUNTS_CONFIG_FILE=<ruta>`), con entradas `type` = `json` | `sqlite` | `refresh_token`, `enabled: true`, y `path`/`refresh_token`. Enlazar a `credentials.json.example`.
- El `.env` guarda `PROXY_API_KEY` + servidor/comportamiento (tabla de "otras variables" que ya está).
- Nota explícita: el port **no** implementa el modo de cuenta única del upstream; `ACCOUNT_SYSTEM` no existe en este port (la gestión de cuentas siempre está activa vía `credentials.json`).

- [ ] **Step 3: `docs/DIFFERENCES.md`**

- Actualizar/retirar las entradas §8 (escapes de dotenv) y §9 (filepath.Clean) para que ya no citen `KIRO_CREDS_FILE`/`KIRO_CLI_DB_FILE` como "las dos rutas leídas del .env"; ahora la ruta leída del `.env` es `ACCOUNTS_CONFIG_FILE` (misma justificación de lectura raw / sin `filepath.Clean`).
- Añadir una entrada nueva: "Modelo de credenciales simplificado" — el port solo carga cuentas desde `ACCOUNTS_CONFIG_FILE` (array `credentials.json`); no implementa el modo de cuenta única (`REFRESH_TOKEN`/`KIRO_CREDS_FILE`/`KIRO_CLI_DB_FILE` sueltos) ni el flag `ACCOUNT_SYSTEM` del upstream. Qué cambia / por qué / impacto.

- [ ] **Step 4: Spec — tabla de config y notas**

En `docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md`:
- Tabla §777-781: quitar las filas `KIRO_CREDS_FILE`, `KIRO_CLI_DB_FILE`, `ACCOUNT_SYSTEM`; dejar `ACCOUNTS_CONFIG_FILE` como el fichero de cuentas (lectura raw-first del `.env`) y `ACCOUNTS_STATE_FILE`.
- §6.1 (punto 1, "Lectura en crudo del .env"): cambiar el ejemplo de `KIRO_CREDS_FILE`/`KIRO_CLI_DB_FILE` a `ACCOUNTS_CONFIG_FILE`.
- §6.11: añadir una frase de que el fichero de cuentas lo nombra `ACCOUNTS_CONFIG_FILE` y que no hay modo de cuenta única en el port.

- [ ] **Step 5: Verificación de enlaces y build de docs**

Run (Bash): comprobar que `credentials.json.example` existe y que los READMEs no dejan enlaces rotos; `go build ./...` sigue verde (no cambió código).
Expected: OK.

- [ ] **Step 6: Commit**

```bash
git add README.md README.en.md credentials.json.example docs/DIFFERENCES.md docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md
git commit -m "docs: modelo de credenciales solo-json (ACCOUNTS_CONFIG_FILE) en README/DIFFERENCES/spec"
```

---

## Self-Review

**1. Cobertura del spec/diseño:** el diseño aprobado tiene tres partes — config (Task 1), accountmanager (Task 2), docs (Task 3). Las tres están cubiertas. El descubrimiento (§6.11) no cambia; solo la variable que lo dirige.

**2. Escaneo de placeholders:** sin TBD/TODO. El código de producción va literal (config.go, discovery.go, manager.go tienen contenido exacto con líneas). Los tests dan el contrato (código de test para los dos casos nuevos) y una **regla de clasificación** explícita para el repunteo mecánico de helpers, cuya red de seguridad real es `go test ./... -count=1` en verde (Task 2, Step 6); esto es correcto para un repunteo dirigido por el compilador y las suites.

**3. Consistencia de tipos:** `Config.AccountsConfigFile`/`AccountsStateFile` son `string` (ya existentes); se retira el campo `bool AccountSystem`. `lookupPath(string) (string,bool)` y `parseString(string,bool,string) string` ya existen en `internal/config`. `discovery` y `manager` consumen `m.cfg.AccountsConfigFile string`. Sin firmas públicas nuevas.

**4. Migración del usuario:** `.env`: `KIRO_CREDS_FILE=…credentials.json` → `ACCOUNTS_CONFIG_FILE=…credentials.json` (o quitarlo y dejar `credentials.json` en el cwd). `credentials.json` no cambia. Documentar en el commit de docs.
