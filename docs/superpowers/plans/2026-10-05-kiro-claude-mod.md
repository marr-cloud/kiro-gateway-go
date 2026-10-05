# Mod de Claude Code para kiro-gateway (nivel 3) — plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Que Claude Code sobre Kiro no se corte: un refusal reintenta solo con el modelo de respaldo que declara Kiro, un gateway caído se relanza solo, y `/kiro` controla el gateway sin salir de la sesión.

**Architecture:** Tres piezas. (1) El gateway Go guarda el `refusalFallbackModels` que ya devuelve `ListAvailableModels` y lo expone, junto a versión/cuenta/debug/capacidades, en `GET /kiro/status`. (2) `scripts/kiro-gateway.ps1 start|stop|restart|logs` es la única lógica de arranque; `kclaude` lo usa y carga el mod con `--plugin-dir`. (3) El mod `claude-mod/` (plugin `kiro`, función hooks de CC 2.1.289) hace el relanzado y el reintento en `turn.step` y sirve `/kiro`.

**Tech Stack:** Go 1.2x (stdlib `net/http`), PowerShell 7 (`pwsh`, CIM `Win32_Process`), TypeScript de mods de Claude Code 2.1.289 (`claude plugin validate|test`, `tsc`), Python vía `uv` solo para el gateway falso del e2e.

**Spec:** `docs/superpowers/specs/2026-10-05-kiro-claude-mod-design.md` (aprobada).

## Global Constraints

- Idioma: comentarios, docs, mensajes de commit y textos de UI en español, como el resto del repo. Commits con el estilo `tipo(ámbito): resumen` y la línea final `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Rama: `feat/claude-mod`. No hacer push. El merge lo decide el usuario.
- Secretos: ninguna key literal en archivos, comandos ni tests reales. La key llega al mod por `ANTHROPIC_AUTH_TOKEN` (la pone `kclaude` desde `.env`) y nunca se escribe en disco. En tests se usan valores falsos (`k-test`, `e2e-key`).
- Cuidar la cuenta de Kiro: la única petición extra a Kiro es un reintento por paso, solo tras un refusal y solo al modelo que Kiro declara. Nunca forzar un refusal real; el reintento se prueba contra el gateway falso (Task 7).
- Go: TDD, cabecera `// SPDX-License-Identifier: AGPL-3.0-or-later` + `// Port a Go de jwadow/kiro-gateway. Ver NOTICE.` en cada archivo nuevo, `go test ./...` y `go vet ./...` en verde al cerrar cada tarea Go.
- Mod: `claude plugin validate claude-mod`, `claude plugin test claude-mod` y `tsc -p claude-mod` en verde al cerrar cada tarea del mod. Los nombres de `$.env.get` van como string literal (lo exige el validador).
- Entrada: se queda `kclaude`. No se tocan los settings globales ni el comando `claude`. Sin barra de estado.

## Hechos verificados al escribir el plan (no repetir)

- **Forma real de `refusalFallbackModels`** (respuesta de `ListAvailableModels` guardada en la sesión de diseño): `"refusalFallbackModels":[{"modelId":"claude-sonnet-5"}]`. Lo traen `claude-sonnet-5.5 → claude-sonnet-5`, `claude-opus-5.5 → claude-opus-4.8` y `claude-opus-5 → claude-opus-4.8`. Los demás no lo traen.
- **Claude Code no tiene un respaldo por refusal configurable para nuestro caso.** Se probó `claude -p --model claude-sonnet-5-5 --fallback-model claude-sonnet-5` contra un servidor Anthropic falso que responde `stop_reason: "refusal"`: hubo 2 peticiones, las dos a `claude-sonnet-5-5` (CC reintenta una vez con el mismo modelo), y terminó con «Claude Code can't respond to this message with Sonnet 5.5». El respaldo nativo que hay en el binario (`refusalFallbackModel`) está ligado a la línea de modelos de Anthropic y a flags internos; desde fuera solo se puede desactivar (`CLAUDE_CODE_DISABLE_REFUSAL_FALLBACK`). Conclusión: **el hook de reintento del mod va adelante**; la verificación previa que pedía la spec queda hecha.
- **`Start-Process` no sirve para lanzar el gateway desde `$.process.run`.** Un script que lanza un proceso largo con `Start-Process -RedirectStandardOutput` y se ejecuta con la salida capturada (`out=$(pwsh -File x.ps1)`) tarda lo que vive el hijo (25 s en la prueba): el hijo hereda los handles del pipe. Con `Invoke-CimMethod Win32_Process Create` + `cmd /c ... > log 2> err` volvió en 1 s y la redirección funcionó. El script usa CIM.

## Ajustes de implementación frente a la spec

- El mapa de respaldos vive en variables del módulo, no en `$.state`: un reload vuelve a lanzar `session.start` y lo rehace, y así el mod no necesita el contrato de tipos que exige `$.state`.
- `/kiro logs` lee los logs a través de `kiro-gateway.ps1 logs`: las rutas de los logs quedan en un solo sitio (el script) y el mod no necesita `$.fs` ni `LOCALAPPDATA`.
- El script lanza el gateway por CIM y no con `Start-Process` (ver «Hechos verificados»).

## Review Focus

1. Ids de modelo en forma de Claude Code (`claude-sonnet-5-5`, `claude-sonnet-5-5[1m]`) frente a los de Kiro (`claude-sonnet-5.5`): el respaldo tiene que encontrarse igual. Test en Task 4 (`modelKey`) y en Task 5 (refusal con id de CC).
2. El modelo de respaldo también corta: no hay un segundo reintento. Test en Task 5.
3. El gateway no arranca (binario ausente, puerto ocupado por otro proceso): toast con el error, un solo intento por paso, y la petición sigue para que Claude Code muestre su error normal. Test en Task 5; puerto ocupado probado a mano en Task 3.
4. La key de `kclaude` no coincide con la del gateway (401): `/kiro` lo dice con claridad en vez de un error genérico. Test en Task 6.
5. Quien ejecuta el script con la salida capturada no se queda colgado esperando al gateway (el problema de `Start-Process`). Prueba de tiempo en Task 3.

---

## Mapa de archivos

| Archivo | Responsabilidad |
| --- | --- |
| `internal/modelcaps/modelcaps.go` (mod.) | Registro de respaldo por refusal: `SetRefusalFallback`, `RefusalFallback`. |
| `internal/accountmanager/modeldiscovery.go` (mod.) | Lee `refusalFallbackModels` y lo registra. |
| `internal/accountmanager/testdata/list_available_models.json` (nuevo) | Fixture real recortado (3 modelos). |
| `internal/server/kirostatus.go` (nuevo) | Handler y tipos de `GET /kiro/status`. |
| `internal/server/server.go`, `middleware.go` (mod.) | Ruta, seam `availableModels`, auth de `/kiro/status`. |
| `docs/DIFFERENCES.md`, `README.md` (mod.) | §20 y la sección de Claude Code. |
| `scripts/kiro-gateway.ps1` (nuevo) | start/stop/restart/logs del gateway. |
| `scripts/kiro-claude.ps1` (mod.) | Delega el arranque y añade `--plugin-dir`. |
| `claude-mod/.claude-plugin/plugin.json`, `claude-mod/hooks/hooks.json`, `claude-mod/tsconfig.json` (nuevos) | Manifiesto y config del mod. |
| `claude-mod/hooks/kiro.ts` (nuevo) | Funciones puras: ids, puerto, formato de tablas y estado, argumentos. |
| `claude-mod/hooks/register.ts` (nuevo) | Hooks: `session.start`, `turn.step`, `command.run` de `/kiro`. |
| `claude-mod/tests/world.ts`, `kiro.test.ts`, `register.test.ts`, `command.test.ts` (nuevos) | Mundo simulado y tests del mod. |
| `claude-mod/e2e/fake_gateway.py` (nuevo) | Gateway falso para el e2e con el motor real. |
| `.gitignore` (mod.) | Tipos que escribe el motor y salidas del e2e. |

---

### Task 1: El discovery guarda el modelo de respaldo por refusal

**Files:**
- Modify: `internal/modelcaps/modelcaps.go` (registro al final del archivo)
- Modify: `internal/accountmanager/modeldiscovery.go:118-136` (struct `parsed` y bucle)
- Create: `internal/accountmanager/testdata/list_available_models.json`
- Test: `internal/modelcaps/modelcaps_test.go`, `internal/accountmanager/modeldiscovery_test.go`

**Interfaces:**
- Produces: `modelcaps.SetRefusalFallback(modelID, fallback string)`, `modelcaps.RefusalFallback(modelID string) string` (`""` si no hay). Normalizan el id igual que `modelcaps.Get` (`claude-sonnet-5-5` ≡ `claude-sonnet-5.5`). `modelcaps.Reset()` también vacía este registro.

- [ ] **Step 1: Test de modelcaps (falla)**

Añade al final de `internal/modelcaps/modelcaps_test.go`:

```go
// El respaldo por refusal se busca con el id normalizado, como Get.
func TestRefusalFallback(t *testing.T) {
	t.Cleanup(Reset)
	Reset()
	SetRefusalFallback("claude-sonnet-5.5", "claude-sonnet-5")

	for _, id := range []string{"claude-sonnet-5.5", "claude-sonnet-5-5", "CLAUDE-SONNET-5.5"} {
		if got := RefusalFallback(id); got != "claude-sonnet-5" {
			t.Errorf("RefusalFallback(%q) = %q, quiero claude-sonnet-5", id, got)
		}
	}
	if got := RefusalFallback("claude-haiku-4.5"); got != "" {
		t.Errorf("modelo sin respaldo: %q, quiero vacío", got)
	}
	Reset()
	if got := RefusalFallback("claude-sonnet-5.5"); got != "" {
		t.Errorf("Reset no vació los respaldos: %q", got)
	}
}
```

- [ ] **Step 2: Comprobar que falla**

Run: `go test ./internal/modelcaps/ -run TestRefusalFallback`
Expected: FAIL de compilación, `undefined: SetRefusalFallback`.

- [ ] **Step 3: Implementar en modelcaps**

En `internal/modelcaps/modelcaps.go`, sustituye el bloque `var ( mu ...; registry ... )` y `Reset` por:

```go
var (
	mu        sync.RWMutex
	registry  = map[string]Caps{}
	fallbacks = map[string]string{}
)
```

Añade después de `Get`:

```go
// SetRefusalFallback registra el modelo con el que Kiro manda reintentar
// cuando modelID corta la respuesta (refusalFallbackModels de
// ListAvailableModels). Lo lee GET /kiro/status para el mod de Claude Code
// (DIFFERENCES §20).
func SetRefusalFallback(modelID, fallback string) {
	mu.Lock()
	defer mu.Unlock()
	fallbacks[key(modelID)] = fallback
}

// RefusalFallback devuelve el modelo de respaldo de modelID, o "" si Kiro no
// declaró ninguno. Normaliza el id igual que Get.
func RefusalFallback(modelID string) string {
	mu.RLock()
	defer mu.RUnlock()
	return fallbacks[key(modelID)]
}
```

Y deja `Reset` así:

```go
// Reset vacía el registro (tests).
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	registry = map[string]Caps{}
	fallbacks = map[string]string{}
}
```

- [ ] **Step 4: Comprobar que pasa**

Run: `go test ./internal/modelcaps/`
Expected: PASS.

- [ ] **Step 5: Fixture real**

Crea `internal/accountmanager/testdata/list_available_models.json` con este contenido exacto (respuesta real de `ListAvailableModels`, recortada a tres modelos):

```json
{"defaultModel":{"modelId":"auto"},"models":[{"additionalModelRequestFieldsSchema":{"type":"object","properties":{"thinking":{"additionalProperties":false,"type":"object","properties":{"type":{"type":"string","enum":["adaptive","between_tools"]},"display":{"type":"string","enum":["summarized","omitted"]}},"required":["type"]},"output_config":{"type":"object","properties":{"effort":{"type":"string","enum":["low","medium","high","xhigh","max"],"default":"high"}}},"max_tokens":{"type":"integer","minimum":1024,"maximum":128000}},"additionalProperties":false},"description":"Experimental preview of Claude Sonnet 5.5 model with 1M context window","modelId":"claude-sonnet-5.5","modelName":"claude-sonnet-5.5","promptCaching":{"supportsPromptCaching":true},"rateMultiplier":1.3,"rateUnit":"Credit","refusalFallbackModels":[{"modelId":"claude-sonnet-5"}],"supportedInputTypes":["TEXT","IMAGE"],"tokenLimits":{"maxInputTokens":1000000,"maxOutputTokens":128000}},{"additionalModelRequestFieldsSchema":{"type":"object","properties":{"thinking":{"type":"object","properties":{"type":{"type":"string","enum":["adaptive","disabled"]},"display":{"type":"string","enum":["summarized","omitted"]}},"required":["type"]},"output_config":{"type":"object","properties":{"effort":{"type":"string","enum":["low","medium","high","xhigh","max"],"default":"high"}}},"max_tokens":{"type":"integer","minimum":1024,"maximum":128000}},"additionalProperties":false},"description":"Claude Opus 5 model with 1M context window","modelId":"claude-opus-5","modelName":"claude-opus-5","promptCaching":{"supportsPromptCaching":true},"rateMultiplier":2.2,"rateUnit":"Credit","refusalFallbackModels":[{"modelId":"claude-opus-4.8"}],"supportedInputTypes":["TEXT","IMAGE"],"tokenLimits":{"maxInputTokens":1000000,"maxOutputTokens":128000}},{"description":"The latest Claude Haiku model","modelId":"claude-haiku-4.5","modelName":"claude-haiku-4.5","promptCaching":{"maximumCacheCheckpointsPerRequest":4,"minimumTokensPerCacheCheckpoint":4096,"supportsPromptCaching":true},"rateMultiplier":0.4,"rateUnit":"Credit","supportedInputTypes":["TEXT","IMAGE"],"tokenLimits":{"maxInputTokens":200000,"maxOutputTokens":64000}}]}
```

- [ ] **Step 6: Test del discovery (falla)**

En `internal/accountmanager/modeldiscovery_test.go` añade `"os"` a los imports y al final del archivo:

```go
// El discovery registra el respaldo por refusal que declara Kiro
// (refusalFallbackModels), con la forma real de la respuesta.
func TestListModelsFromManagementRegistersRefusalFallback(t *testing.T) {
	t.Cleanup(modelcaps.Reset)
	modelcaps.Reset()
	raw, err := os.ReadFile("testdata/list_available_models.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	m, acc := newRuntimeManager(t, func(string) string { return srv.URL + "/" })
	ids, err := m.listModelsFromManagement(context.Background(), acc)
	if err != nil {
		t.Fatalf("listModelsFromManagement: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("ids = %v, quiero 3", ids)
	}
	for model, want := range map[string]string{
		"claude-sonnet-5.5": "claude-sonnet-5",
		"claude-sonnet-5-5": "claude-sonnet-5", // id en forma de Claude Code
		"claude-opus-5":     "claude-opus-4.8",
		"claude-haiku-4.5":  "",
	} {
		if got := modelcaps.RefusalFallback(model); got != want {
			t.Errorf("RefusalFallback(%q) = %q, quiero %q", model, got, want)
		}
	}
	caps, ok := modelcaps.Get("claude-sonnet-5.5")
	if !ok || len(caps.ThinkingTypes) != 2 || caps.ThinkingTypes[1] != "between_tools" {
		t.Errorf("caps de claude-sonnet-5.5 = %+v, %v", caps, ok)
	}
}
```

- [ ] **Step 7: Comprobar que falla**

Run: `go test ./internal/accountmanager/ -run TestListModelsFromManagementRegistersRefusalFallback`
Expected: FAIL, `RefusalFallback("claude-sonnet-5.5") = "", quiero "claude-sonnet-5"`.

- [ ] **Step 8: Implementar en el discovery**

En `internal/accountmanager/modeldiscovery.go`, el struct `parsed` queda:

```go
	var parsed struct {
		Models []struct {
			ModelID  string          `json:"modelId"`
			Schema   json.RawMessage `json:"additionalModelRequestFieldsSchema"`
			Fallback []struct {
				ModelID string `json:"modelId"`
			} `json:"refusalFallbackModels"`
		} `json:"models"`
		NextToken string `json:"nextToken"`
	}
```

Y dentro del `if mdl.ModelID != "" {`, después del bloque de `modelcaps.ParseSchema`:

```go
			// Modelo con el que Kiro manda reintentar un corte (DIFFERENCES
			// §20). Kiro declara uno; si algún día declara varios, vale el primero.
			if len(mdl.Fallback) > 0 && mdl.Fallback[0].ModelID != "" {
				modelcaps.SetRefusalFallback(mdl.ModelID, mdl.Fallback[0].ModelID)
			}
```

Actualiza también el comentario de `fetchManagementModelsPage`: «Registra además en modelcaps las capacidades de razonamiento y el modelo de respaldo por refusal de cada modelo.»

- [ ] **Step 9: Comprobar que pasa todo**

Run: `go test ./... && go vet ./...`
Expected: PASS, sin avisos de vet.

- [ ] **Step 10: Commit**

```bash
git add internal/modelcaps internal/accountmanager
git commit -m "feat(discovery): guarda el modelo de respaldo por refusal de Kiro

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: `GET /kiro/status`

**Files:**
- Create: `internal/server/kirostatus.go`
- Create: `internal/server/kirostatus_test.go`
- Modify: `internal/server/server.go` (campo `availableModels`, `New`, `buildHandler`)
- Modify: `internal/server/middleware.go` (`classify`)
- Modify: `docs/DIFFERENCES.md` (nuevo §20 antes de «## Comportamientos del original que se replican a propósito»), `README.md` (tabla de «Referencia de API»)

**Interfaces:**
- Consumes: `modelcaps.Get`, `modelcaps.RefusalFallback` (Task 1); `accountmanager.Manager.GetAllAvailableModels() []string`.
- Produces (contrato con el mod, Tasks 4-6): `GET /kiro/status`, auth `Authorization: Bearer <PROXY_API_KEY>` o `x-api-key`, 401 con sobre de error Anthropic si falla. Cuerpo:

```json
{"version":"…","uptime_seconds":12.3,"active_account":"…"|null,
 "debug":{"mode":"off|errors|all","dir":"<ruta absoluta>"},
 "models":[{"id":"claude-sonnet-5.5","native_thinking":["adaptive","between_tools"],
            "effort_levels":["low","…"],"refusal_fallback":"claude-sonnet-5"}]}
```

`native_thinking` y `effort_levels` son siempre listas (nunca `null`); `refusal_fallback` es `""` si no hay. `models` sigue el orden de `GetAllAvailableModels` (alfabético).

- [ ] **Step 1: Tests (fallan)**

Crea `internal/server/kirostatus_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelcaps"
	"github.com/marr-cloud/kiro-gateway-go/internal/version"
)

func TestKiroStatus_NoAuth_Returns401(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/kiro/status", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, quiero 401", rec.Code)
	}
	rec = doRequest(s, http.MethodGet, "/kiro/status", map[string]string{"x-api-key": "otra"}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("key incorrecta: status = %d, quiero 401", rec.Code)
	}
}

func TestKiroStatus_ReturnsModelsAndDebug(t *testing.T) {
	t.Cleanup(modelcaps.Reset)
	modelcaps.Reset()
	modelcaps.Set("claude-sonnet-5.5", modelcaps.Caps{
		ThinkingTypes: []string{"adaptive", "between_tools"},
		EffortPath:    "output_config",
		EffortLevels:  []string{"low", "high"},
	})
	modelcaps.SetRefusalFallback("claude-sonnet-5.5", "claude-sonnet-5")

	s := newTestServer(t)
	s.cfg.DebugMode = "errors"
	s.cfg.DebugDir = "debug_logs"
	s.availableModels = func() []string { return []string{"claude-haiku-4.5", "claude-sonnet-5.5"} }

	for _, hdr := range []map[string]string{
		{"Authorization": "Bearer " + testAPIKey},
		{"x-api-key": testAPIKey},
	} {
		rec := doRequest(s, http.MethodGet, "/kiro/status", hdr, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%v: status = %d, cuerpo %s", hdr, rec.Code, rec.Body.String())
		}
		var got kiroStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Version != version.Version() {
			t.Errorf("version = %q", got.Version)
		}
		if got.ActiveAccount == nil || *got.ActiveAccount == "" {
			t.Errorf("active_account vacío")
		}
		if got.UptimeSeconds <= 0 {
			t.Errorf("uptime_seconds = %v", got.UptimeSeconds)
		}
		if got.Debug.Mode != "errors" || !filepath.IsAbs(got.Debug.Dir) || filepath.Base(got.Debug.Dir) != "debug_logs" {
			t.Errorf("debug = %+v, quiero mode errors y dir absoluto acabado en debug_logs", got.Debug)
		}
		if len(got.Models) != 2 {
			t.Fatalf("models = %+v", got.Models)
		}
		haiku, sonnet := got.Models[0], got.Models[1]
		if haiku.ID != "claude-haiku-4.5" || len(haiku.NativeThinking) != 0 || len(haiku.EffortLevels) != 0 || haiku.RefusalFallback != "" {
			t.Errorf("haiku = %+v", haiku)
		}
		if sonnet.ID != "claude-sonnet-5.5" || sonnet.RefusalFallback != "claude-sonnet-5" ||
			strings.Join(sonnet.NativeThinking, ",") != "adaptive,between_tools" || strings.Join(sonnet.EffortLevels, ",") != "low,high" {
			t.Errorf("sonnet = %+v", sonnet)
		}
		// Listas vacías como [] (el mod hace .join sobre ellas).
		if !strings.Contains(rec.Body.String(), `"native_thinking":[]`) {
			t.Errorf("native_thinking vacío no sale como []: %s", rec.Body.String())
		}
	}
}
```

- [ ] **Step 2: Comprobar que fallan**

Run: `go test ./internal/server/ -run TestKiroStatus`
Expected: FAIL de compilación, `undefined: kiroStatusResponse` y `s.availableModels undefined`.

- [ ] **Step 3: Handler**

Crea `internal/server/kirostatus.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package server

import (
	"net/http"
	"path/filepath"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelcaps"
	"github.com/marr-cloud/kiro-gateway-go/internal/version"
)

// kiroStatusResponse es el JSON de GET /kiro/status: lo que el mod de Claude
// Code (claude-mod/) necesita para /kiro y para reintentar un corte con el
// modelo de respaldo. Sin equivalente en el original (DIFFERENCES §20). No
// sale de la máquina: lee lo que el discovery ya guardó.
type kiroStatusResponse struct {
	Version       string            `json:"version"`
	UptimeSeconds float64           `json:"uptime_seconds"`
	ActiveAccount *string           `json:"active_account"`
	Debug         kiroStatusDebug   `json:"debug"`
	Models        []kiroStatusModel `json:"models"`
}

type kiroStatusDebug struct {
	Mode string `json:"mode"`
	Dir  string `json:"dir"` // absoluto, resuelto desde el directorio de trabajo
}

type kiroStatusModel struct {
	ID              string   `json:"id"`
	NativeThinking  []string `json:"native_thinking"`
	EffortLevels    []string `json:"effort_levels"`
	RefusalFallback string   `json:"refusal_fallback"`
}

func (s *Server) handleKiroStatus(w http.ResponseWriter, r *http.Request) {
	dir, err := filepath.Abs(s.cfg.DebugDir)
	if err != nil {
		dir = s.cfg.DebugDir
	}
	ids := s.availableModels()
	models := make([]kiroStatusModel, 0, len(ids))
	for _, id := range ids {
		caps, _ := modelcaps.Get(id)
		models = append(models, kiroStatusModel{
			ID:              id,
			NativeThinking:  orEmpty(caps.ThinkingTypes),
			EffortLevels:    orEmpty(caps.EffortLevels),
			RefusalFallback: modelcaps.RefusalFallback(id),
		})
	}
	writeJSON(w, http.StatusOK, kiroStatusResponse{
		Version:       version.Version(),
		UptimeSeconds: s.uptimeSeconds(),
		ActiveAccount: s.activeAccountID(),
		Debug:         kiroStatusDebug{Mode: s.cfg.DebugMode, Dir: dir},
		Models:        models,
	})
}

// orEmpty devuelve [] en vez de nil para que el JSON lleve una lista.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
```

- [ ] **Step 4: Cablear ruta, seam y auth**

En `internal/server/server.go`:

1. En el struct `Server`, tras `anthropicCountTokens  http.HandlerFunc`, añade:

```go
	// availableModels es la lista de modelos de GET /kiro/status. Seam de
	// test con el mismo patrón que los handlers de arriba.
	availableModels func() []string
```

2. En `New`, después de `s.anthropicCountTokens = anthropicHandler.CountTokens`:

```go
	s.availableModels = accounts.GetAllAvailableModels
```

3. En `buildHandler`, después de la línea de `POST /v1/messages/count_tokens`:

```go
	mux.HandleFunc("GET /kiro/status", s.handleKiroStatus)
```

En `internal/server/middleware.go`, `classify` queda:

```go
// classify decide el dialect de un path. Los paths fuera de las rutas
// conocidas (incluida cualquier 404 futura) caen en dialectPublic: sin auth,
// y el 500 de panic recovery (poco probable en esas rutas) usa el dialecto
// OpenAI como fallback genérico — ver writeErrorForPath. /kiro/status
// (DIFFERENCES §20) acepta las dos cabeceras, como la ruta Anthropic.
func classify(path string) dialect {
	switch path {
	case "/v1/models", "/v1/chat/completions":
		return dialectOpenAI
	case "/v1/messages", "/v1/messages/count_tokens", "/kiro/status":
		return dialectAnthropic
	default:
		return dialectPublic
	}
}
```

- [ ] **Step 5: Comprobar que pasa todo**

Run: `go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 6: Documentación**

En `docs/DIFFERENCES.md`, inserta antes de `## Comportamientos del original que se replican a propósito` (con el `---` que separa las secciones, igual que §19):

```markdown
### 20. `GET /kiro/status` y el modelo de respaldo por refusal

**Qué cambia:** endpoint nuevo, sin equivalente en el original, protegido con `PROXY_API_KEY`
(`Authorization: Bearer` o `x-api-key`). Devuelve la versión, el uptime, la cuenta activa, el
`DEBUG_MODE` con su carpeta absoluta y, por modelo, el razonamiento nativo (§18) y el modelo de
respaldo por refusal. Ese respaldo sale de `refusalFallbackModels`, un campo de
`ListAvailableModels` que el original no lee (`claude-sonnet-5.5 → claude-sonnet-5`,
`claude-opus-5.5` y `claude-opus-5 → claude-opus-4.8`).

**Impacto:** lo usa el mod de Claude Code (`claude-mod/`) para `/kiro` y para reintentar un corte
(§17) con el modelo que declara Kiro. No hace peticiones a Kiro: lee lo que guardó el discovery.
Las rutas existentes no cambian.

---
```

En `README.md`, en la tabla de «Referencia de API», añade tras la fila de `/v1/messages/count_tokens`:

```markdown
| `/kiro/status` | GET | Estado para el mod de Claude Code: versión, cuenta, debug y modelos con su respaldo por refusal |
```

- [ ] **Step 7: Commit**

```bash
git add internal/server docs/DIFFERENCES.md README.md
git commit -m "feat(server): GET /kiro/status para el mod de Claude Code

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: `scripts/kiro-gateway.ps1` y `kclaude` delegando en él

Script de infra: cambio directo + verificación manual (no hay tests de PowerShell en el repo).

**Files:**
- Create: `scripts/kiro-gateway.ps1`
- Modify: `scripts/kiro-claude.ps1`
- Modify: `README.md` (sección nueva en «Inicio rápido»)

**Interfaces:**
- Produces (contrato con el mod): `pwsh -NoProfile -NonInteractive -File scripts/kiro-gateway.ps1 <start|stop|restart|logs> [-DebugMode all|errors|off] [-Port N] [-Lines N]`. Escribe en stdout (una línea en start/stop/restart; las colas de los dos logs en `logs`) y sale con 0 o 1. En error escribe `error: <mensaje>`. Sin `-DebugMode` el gateway toma `DEBUG_MODE` de su `.env`; con él, la variable del proceso gana (precedencia shell > .env de `internal/config`).

- [ ] **Step 1: Escribir el script**

Crea `scripts/kiro-gateway.ps1`:

```powershell
<#
.SYNOPSIS
  Arranca, para o reinicia el kiro-gateway local, o muestra sus logs.

.DESCRIPTION
  Unica logica de arranque del gateway: la usan kclaude (scripts/kiro-claude.ps1)
  y el mod de Claude Code (claude-mod/: /kiro y el relanzado automatico).
  Escribe una linea legible y sale con 0 (bien) o 1 (fallo).

  El gateway se lanza con Win32_Process.Create (CIM), no con Start-Process:
  Start-Process le pasa al gateway los handles heredados de este proceso, y
  quien capture la salida del script (el mod, con $.process.run) se quedaria
  esperando hasta que el gateway muriera. Asi queda separado: sobrevive a
  claude y a esta consola.

  Variables opcionales:
    KIRO_GATEWAY_PORT  puerto por defecto (8000)
    KIRO_GATEWAY_EXE   ruta al binario (default <repo>\kiro-gateway.exe)

.EXAMPLE
  .\scripts\kiro-gateway.ps1 start
  .\scripts\kiro-gateway.ps1 restart -DebugMode all
  .\scripts\kiro-gateway.ps1 logs -Lines 50
#>
param(
    [Parameter(Mandatory, Position = 0)]
    [ValidateSet('start', 'stop', 'restart', 'logs')]
    [string]$Action,
    [ValidateSet('all', 'errors', 'off')]
    [string]$DebugMode,
    [int]$Port = $(if ($env:KIRO_GATEWAY_PORT) { [int]$env:KIRO_GATEWAY_PORT } else { 8000 }),
    [int]$Lines = 20
)
$ErrorActionPreference = 'Stop'

$root = Split-Path $PSScriptRoot -Parent
$exe = if ($env:KIRO_GATEWAY_EXE) { $env:KIRO_GATEWAY_EXE } else { Join-Path $root 'kiro-gateway.exe' }
$base = "http://127.0.0.1:$Port"
$logDir = Join-Path $env:LOCALAPPDATA 'kiro-gateway'
$log = Join-Path $logDir 'gateway.log'
$errLog = Join-Path $logDir 'gateway.err.log'

function Test-Gateway {
    try { (Invoke-WebRequest "$base/health" -TimeoutSec 2 -UseBasicParsing).StatusCode -eq 200 } catch { $false }
}

# Proceso que escucha en el puerto, o $null. Falla si no es kiro-gateway: nunca
# se para un proceso ajeno.
function Get-GatewayProcess {
    $conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $conn) { return $null }
    $proc = Get-Process -Id $conn.OwningProcess -ErrorAction SilentlyContinue
    $name = [IO.Path]::GetFileNameWithoutExtension($exe)
    if ($proc -and $proc.ProcessName -ne $name) {
        throw "El puerto $Port lo usa $($proc.ProcessName) (pid $($proc.Id)), no $name."
    }
    return $proc
}

function Start-Gateway {
    if (Test-Gateway) { return "kiro-gateway ya corre en $base" }
    if (-not (Test-Path $exe)) { throw "No encuentro $exe. Compilalo con: task build" }
    New-Item -ItemType Directory -Force $logDir | Out-Null
    # El gateway lee .env y credentials.json del directorio de trabajo.
    $envPrefix = if ($DebugMode) { "set DEBUG_MODE=$DebugMode&& " } else { '' }
    $cmd = "cmd.exe /d /c `"$envPrefix`"$exe`" --host 127.0.0.1 --port $Port > `"$log`" 2> `"$errLog`"`""
    $startup = New-CimInstance -ClassName Win32_ProcessStartup -ClientOnly -Property @{ ShowWindow = [uint16]0 }
    $r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{
        CommandLine = $cmd; CurrentDirectory = $root; ProcessStartupInformation = $startup
    }
    if ($r.ReturnValue -ne 0) { throw "Win32_Process.Create devolvio $($r.ReturnValue)" }
    $deadline = (Get-Date).AddSeconds(20)
    while (-not (Test-Gateway)) {
        if ((Get-Date) -gt $deadline) { throw "kiro-gateway no respondio en $base/health. Revisa $errLog" }
        Start-Sleep -Milliseconds 300
    }
    $mode = if ($DebugMode) { " (DEBUG_MODE=$DebugMode)" } else { '' }
    return "kiro-gateway listo en $base$mode"
}

function Stop-Gateway {
    $proc = Get-GatewayProcess
    if (-not $proc) { return "kiro-gateway no corria en $base" }
    Stop-Process -Id $proc.Id -Force
    $deadline = (Get-Date).AddSeconds(10)
    while (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue) {
        if ((Get-Date) -gt $deadline) { throw "El puerto $Port sigue ocupado tras parar kiro-gateway." }
        Start-Sleep -Milliseconds 200
    }
    return "kiro-gateway parado"
}

function Show-Logs {
    foreach ($f in $log, $errLog) {
        "== $f"
        if (Test-Path $f) { Get-Content $f -Tail $Lines } else { '(no existe)' }
    }
}

try {
    switch ($Action) {
        'start' { Start-Gateway }
        'stop' { Stop-Gateway }
        'restart' { Stop-Gateway | Out-Null; Start-Gateway }
        'logs' { Show-Logs }
    }
    exit 0
} catch {
    "error: $($_.Exception.Message)"
    exit 1
}
```

- [ ] **Step 2: kclaude delega en el script y carga el mod**

En `scripts/kiro-claude.ps1`:

1. En `.DESCRIPTION`, cambia el primer párrafo por:

```
  Arranca kiro-gateway en segundo plano con scripts/kiro-gateway.ps1 si
  /health no responde, y ejecuta `claude` con el mod claude-mod/ cargado
  (--plugin-dir) y las variables de entorno del gateway puestas SOLO para ese
  proceso: la sesion de PowerShell queda como estaba al salir. La key se lee
  de PROXY_API_KEY en el .env del repo; nunca se escribe en settings.
```

2. Borra las líneas de `$exe`, `$base` y `$logDir`, la función `Test-Gateway` y todo el bloque `if (-not (Test-Gateway)) { ... }`. En su lugar, después de `Get-ProxyApiKey`:

```powershell
& (Join-Path $PSScriptRoot 'kiro-gateway.ps1') start -Port $port
if ($LASTEXITCODE -ne 0) { exit 1 }
```

3. En `$vars`, `ANTHROPIC_BASE_URL = "http://127.0.0.1:$port"`.

4. La llamada a claude queda:

```powershell
    & claude --plugin-dir (Join-Path $root 'claude-mod') @args
```

(`KIRO_GATEWAY_EXE` sigue documentado en la cabecera de kclaude: ahora lo lee `kiro-gateway.ps1`, que hereda el entorno.)

- [ ] **Step 3: Verificación manual del script**

Compila y prueba (el `.env` y `credentials.json` del repo deben existir; esto **no** hace peticiones de chat a Kiro, solo el discovery de arranque del gateway):

```bash
task build
pwsh -NoProfile -File scripts/kiro-gateway.ps1 stop      # deja el puerto libre
s=$(date +%s); out=$(pwsh -NoProfile -NonInteractive -File scripts/kiro-gateway.ps1 start); echo "[$out] exit=$? en $(( $(date +%s)-s ))s"
```

Expected: `[kiro-gateway listo en http://127.0.0.1:8000] exit=0` en **menos de 20 s**: la salida capturada no espera al gateway (Review Focus 5). Si tarda ~el timeout o no vuelve, el lanzamiento está heredando handles: revisar que se usa `Invoke-CimMethod` y no `Start-Process`.

```bash
pwsh -NoProfile -File scripts/kiro-gateway.ps1 start; echo "exit=$?"          # idempotente
curl -s http://127.0.0.1:8000/health
pwsh -NoProfile -File scripts/kiro-gateway.ps1 restart -DebugMode errors; echo "exit=$?"
curl -s -H "x-api-key: $(grep -E '^\s*PROXY_API_KEY\s*=' .env | sed -E 's/^\s*PROXY_API_KEY\s*=\s*//; s/["'"'"']//g' | tr -d '\r')" http://127.0.0.1:8000/kiro/status | head -c 400; echo
pwsh -NoProfile -File scripts/kiro-gateway.ps1 logs -Lines 5; echo "exit=$?"
pwsh -NoProfile -File scripts/kiro-gateway.ps1 stop; echo "exit=$?"
pwsh -NoProfile -File scripts/kiro-gateway.ps1 stop; echo "exit=$?"
```

Expected, en orden: `kiro-gateway ya corre…` exit=0; JSON de health; `kiro-gateway listo… (DEBUG_MODE=errors)` exit=0; `/kiro/status` con `"debug":{"mode":"errors",…}` y modelos con `refusal_fallback` (`claude-sonnet-5.5` → `claude-sonnet-5`); las dos cabeceras `== …gateway.log` / `== …gateway.err.log` con hasta 5 líneas cada una, exit=0; `kiro-gateway parado` exit=0; `kiro-gateway no corria…` exit=0. Ninguna ventana visible durante el arranque.

Puerto ocupado por otro proceso (Review Focus 3):

```bash
uv run --no-project python -m http.server 8011 --bind 127.0.0.1 >/dev/null 2>&1 &
sleep 2
pwsh -NoProfile -File scripts/kiro-gateway.ps1 stop -Port 8011; echo "exit=$?"
```

Expected: `error: El puerto 8011 lo usa python (pid …), no kiro-gateway.` exit=1, y el servidor de Python sigue vivo. Párelo después con `pwsh -NoProfile -Command "Stop-Process -Id (Get-NetTCPConnection -LocalPort 8011 -State Listen).OwningProcess"`.

Binario ausente:

```bash
KIRO_GATEWAY_EXE=C:/no/existe.exe pwsh -NoProfile -File scripts/kiro-gateway.ps1 start -Port 8012; echo "exit=$?"
```

Expected: `error: No encuentro C:/no/existe.exe. Compilalo con: task build` exit=1.

- [ ] **Step 4: README**

En `README.md`, inserta antes de `### Flags de línea de comandos`:

````markdown
### Con Claude Code (`kclaude` y `/kiro`)

En Windows, `scripts/kiro-claude.ps1` arranca el gateway si no está corriendo y abre Claude Code
apuntando a él, con el mod [`claude-mod/`](claude-mod) cargado. La key sale de `PROXY_API_KEY` en
el `.env` y solo vive en el entorno de ese proceso.

```powershell
Set-Alias kclaude C:\ruta\a\kiro-gateway\scripts\kiro-claude.ps1   # en tu $PROFILE
kclaude
```

El mod reintenta una sola vez con el modelo de respaldo que declara Kiro cuando un modelo corta la
respuesta (por ejemplo `claude-sonnet-5.5 → claude-sonnet-5`), relanza el gateway si se cayó y
añade `/kiro`:

| Comando | Hace |
| --- | --- |
| `/kiro` | Versión, uptime, cuenta activa, debug y modelo de la sesión |
| `/kiro restart` | Reinicia el gateway conservando su `DEBUG_MODE` |
| `/kiro logs [n]` | Últimas `n` líneas (20) de `gateway.log` y `gateway.err.log` |
| `/kiro debug all\|errors\|off` | Reinicia con ese `DEBUG_MODE` y muestra la carpeta de debug |
| `/kiro models [id]` | Tabla de modelos (thinking nativo, effort, respaldo) o cambia el modelo de la sesión |

`scripts/kiro-gateway.ps1 start|stop|restart|logs` hace lo mismo desde la terminal.
````

- [ ] **Step 5: Commit**

```bash
git add scripts/kiro-gateway.ps1 scripts/kiro-claude.ps1 README.md
git commit -m "feat(scripts): kiro-gateway.ps1 start/stop/restart/logs; kclaude carga el mod

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Esqueleto del mod y funciones puras

**Files:**
- Create: `claude-mod/.claude-plugin/plugin.json`, `claude-mod/hooks/hooks.json`, `claude-mod/hooks/register.ts` (mínimo), `claude-mod/hooks/kiro.ts`, `claude-mod/tsconfig.json`
- Create: `claude-mod/tests/kiro.test.ts`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: el JSON de `GET /kiro/status` (Task 2).
- Produces (para Tasks 5-6), en `claude-mod/hooks/kiro.ts`:
  - `type ModelInfo = { id: string; native_thinking: string[]; effort_levels: string[]; refusal_fallback: string }`
  - `type Status = { version: string; uptime_seconds: number; active_account: string | null; debug: { mode: string; dir: string }; models: ModelInfo[] }`
  - `modelKey(id: string): string` — clave común para ids de CC y de Kiro.
  - `fallbackMap(models: readonly ModelInfo[]): Map<string, string>` — `modelKey(id) → refusal_fallback`, solo los que tienen.
  - `localBase(url: string | undefined): { base: string; port: string } | undefined` — solo `http://127.0.0.1:N` o `http://localhost:N`.
  - `parseArgs(args: string): { sub: string; rest: string[] }`
  - `formatStatus(st: Status, sessionModel: string): string`, `formatModels(models: readonly ModelInfo[]): string`
  - `USAGE: string`

- [ ] **Step 1: Manifiesto, módulo mínimo, tsconfig y .gitignore**

`claude-mod/.claude-plugin/plugin.json`:

```json
{
  "name": "kiro",
  "version": "0.1.0",
  "description": "Recupera los cortes de Kiro, relanza kiro-gateway y añade /kiro."
}
```

`claude-mod/hooks/hooks.json`:

```json
{ "modules": ["./register.ts"] }
```

`claude-mod/hooks/register.ts` (mínimo, lo completa la Task 5):

```ts
import type { Register } from 'claude-code'

export const register: Register = () => {}
```

`claude-mod/tsconfig.json` (el del encabezado de los tipos de CC 2.1.289, más `allowImportingTsExtensions` porque los módulos se importan con su sufijo `.ts`):

```json
{
  "compilerOptions": {
    "target": "es2023", "lib": ["es2023"], "types": [],
    "module": "esnext", "moduleResolution": "bundler",
    "strict": true, "noUncheckedIndexedAccess": true,
    "noEmit": true, "skipLibCheck": true, "allowImportingTsExtensions": true,
    "jsx": "react", "jsxFactory": "h", "jsxFragmentFactory": "Fragment"
  },
  "include": [".claude-plugin/types", "hooks", "tests"]
}
```

Añade al final de `.gitignore`:

```
# Mod de Claude Code: tipos que escribe el motor al cargarlo y salidas del e2e
claude-mod/.claude-plugin/types/
claude-mod/e2e/*.log
claude-mod/e2e/*.json
```

- [ ] **Step 2: Tests de las funciones puras (fallan)**

`claude-mod/tests/kiro.test.ts`:

```ts
import { describe, expect, test } from 'claude-code/testing'
import { fallbackMap, formatModels, formatStatus, localBase, modelKey, parseArgs, type ModelInfo, type Status } from '../hooks/kiro.ts'

const MODELS: ModelInfo[] = [
  { id: 'claude-haiku-4.5', native_thinking: [], effort_levels: [], refusal_fallback: '' },
  { id: 'claude-sonnet-5.5', native_thinking: ['adaptive', 'between_tools'], effort_levels: ['low', 'high'], refusal_fallback: 'claude-sonnet-5' },
]

describe('kiro.ts', () => {
  test('modelKey iguala ids de Claude Code y de Kiro', () => {
    expect(modelKey('claude-sonnet-5.5')).toBe('claude-sonnet-5-5')
    expect(modelKey('claude-sonnet-5-5')).toBe('claude-sonnet-5-5')
    expect(modelKey('Claude-Sonnet-5-5[1m]')).toBe('claude-sonnet-5-5')
  })

  test('fallbackMap solo guarda los modelos con respaldo', () => {
    const m = fallbackMap(MODELS)
    expect(m.size).toBe(1)
    expect(m.get('claude-sonnet-5-5')).toBe('claude-sonnet-5')
  })

  test('localBase acepta solo un gateway local', () => {
    expect(localBase('http://127.0.0.1:8000')).toEqual({ base: 'http://127.0.0.1:8000', port: '8000' })
    expect(localBase('http://localhost:9001/')).toEqual({ base: 'http://localhost:9001', port: '9001' })
    expect(localBase('https://api.anthropic.com')).toBeUndefined()
    expect(localBase('http://10.0.0.5:8000')).toBeUndefined()
    expect(localBase(undefined)).toBeUndefined()
  })

  test('parseArgs separa el subcomando', () => {
    expect(parseArgs('')).toEqual({ sub: '', rest: [] })
    expect(parseArgs('  logs   50 ')).toEqual({ sub: 'logs', rest: ['50'] })
  })

  test('formatStatus y formatModels', () => {
    const st: Status = { version: 'v0.4.0', uptime_seconds: 3725, active_account: 'acc1', debug: { mode: 'errors', dir: 'C:\\kg\\debug_logs' }, models: MODELS }
    const s = formatStatus(st, 'claude-sonnet-5-5')
    expect(s).toContain('kiro-gateway v0.4.0')
    expect(s).toContain('1 h 2 min')
    expect(s).toContain('acc1')
    expect(s).toContain('errors (C:\\kg\\debug_logs)')
    expect(s).toContain('claude-sonnet-5-5')
    const t = formatModels(MODELS).split('\n')
    expect(t).toHaveLength(3)
    expect(t[0]).toMatch(/^modelo\s+thinking nativo\s+effort\s+respaldo$/)
    expect(t[1]).toMatch(/^claude-haiku-4\.5\s+-\s+-\s+-$/)
    expect(t[2]).toMatch(/^claude-sonnet-5\.5\s+adaptive,between_tools\s+low,high\s+claude-sonnet-5$/)
  })
})
```

- [ ] **Step 3: Comprobar que fallan**

Run: `claude plugin test claude-mod`
Expected: FAIL, el test no encuentra `../hooks/kiro.ts`.

- [ ] **Step 4: Implementar `kiro.ts`**

`claude-mod/hooks/kiro.ts`:

```ts
// Funciones puras del mod: sin `$`, para que los tests las prueben sueltas.

/** Un modelo tal como lo devuelve GET /kiro/status. */
export type ModelInfo = {
  id: string
  native_thinking: string[]
  effort_levels: string[]
  refusal_fallback: string
}

/** El cuerpo de GET /kiro/status (DIFFERENCES §20 del gateway). */
export type Status = {
  version: string
  uptime_seconds: number
  active_account: string | null
  debug: { mode: string; dir: string }
  models: ModelInfo[]
}

export const USAGE = 'Uso: /kiro [restart | logs [n] | debug all|errors|off | models [id]]'

/**
 * Clave común para los ids de Claude Code (claude-sonnet-5-5, con o sin
 * sufijo [1m]) y los de Kiro (claude-sonnet-5.5).
 */
export function modelKey(id: string): string {
  return id.trim().toLowerCase().replace(/\[[^\]]*\]$/, '').replace(/\./g, '-')
}

/** modelKey(id) → modelo de respaldo, solo para los modelos que lo declaran. */
export function fallbackMap(models: readonly ModelInfo[]): Map<string, string> {
  const map = new Map<string, string>()
  for (const m of models) if (m.refusal_fallback) map.set(modelKey(m.id), m.refusal_fallback)
  return map
}

/** La base y el puerto si url es un gateway local; undefined en otro caso. */
export function localBase(url: string | undefined): { base: string; port: string } | undefined {
  const m = /^http:\/\/(127\.0\.0\.1|localhost):(\d+)\/?$/.exec(url ?? '')
  if (!m) return undefined
  return { base: `http://${m[1]}:${m[2]}`, port: m[2]! }
}

export function parseArgs(args: string): { sub: string; rest: string[] } {
  const parts = args.trim().split(/\s+/).filter(Boolean)
  return { sub: parts[0] ?? '', rest: parts.slice(1) }
}

function formatUptime(seconds: number): string {
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return h > 0 ? `${h} h ${m} min` : `${m} min`
}

export function formatStatus(st: Status, sessionModel: string): string {
  return [
    `kiro-gateway ${st.version} · activo hace ${formatUptime(st.uptime_seconds)}`,
    `cuenta: ${st.active_account ?? '(ninguna)'}`,
    `debug: ${st.debug.mode} (${st.debug.dir})`,
    `modelo de la sesión: ${sessionModel}`,
  ].join('\n')
}

export function formatModels(models: readonly ModelInfo[]): string {
  const head = ['modelo', 'thinking nativo', 'effort', 'respaldo']
  const rows = models.map(m => [
    m.id,
    m.native_thinking.join(',') || '-',
    m.effort_levels.join(',') || '-',
    m.refusal_fallback || '-',
  ])
  const all = [head, ...rows]
  const widths = head.map((_, i) => Math.max(...all.map(r => r[i]!.length)))
  return all.map(r => r.map((c, i) => c.padEnd(widths[i]!)).join('  ').trimEnd()).join('\n')
}
```

- [ ] **Step 5: Comprobar que pasan, validar y tipar**

Run: `claude plugin test claude-mod && claude plugin validate claude-mod`
Expected: PASS y validación sin errores.

Si no existe `claude-mod/.claude-plugin/types/claude-code/index.d.ts` tras esos comandos, cópialo de los tipos que escribe la skill plugin-authoring (es el mismo archivo; está en `.gitignore`):

```bash
src=$(ls -t "$LOCALAPPDATA"/Temp/claude/bundled-skills/*/*/plugin-authoring/types/claude-code.d.ts | head -1)
mkdir -p claude-mod/.claude-plugin/types/claude-code && cp "$src" claude-mod/.claude-plugin/types/claude-code/index.d.ts
```

Run: `tsc -p claude-mod`
Expected: sin errores.

- [ ] **Step 6: Commit**

```bash
git add .gitignore claude-mod
git commit -m "feat(mod): esqueleto del mod kiro y funciones puras

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Arranque, gateway caído y reintento por refusal

**Files:**
- Modify: `claude-mod/hooks/register.ts`
- Create: `claude-mod/tests/world.ts` (mundo simulado bajo el mod; sin `.test` para que no se ejecute como test al importarlo)
- Create: `claude-mod/tests/register.test.ts`

**Interfaces:**
- Consumes: `kiro.ts` (Task 4); `GET /health` y `GET /kiro/status` (Task 2); `scripts/kiro-gateway.ps1` (Task 3), resuelto como `<raíz del mod>/../scripts/kiro-gateway.ps1`.
- Produces para Task 6, en `tests/world.ts`: `STATUS`, `type World`, `world(on, opts?)`, `start($)`.
- Produces (para Task 6, dentro del closure de `register`): `state` (`{ base, port, token, fallbacks }`), `healthy($)`, `fetchStatus($): Promise<Status>` (lanza `Error` con «401: …» en un 401), `runScript($, args): Promise<{ ok: boolean; text: string }>`.

Comportamiento (spec, «Mod»):
- `session.start`: si `ANTHROPIC_BASE_URL` no es un gateway local, el mod queda inerte (ni comando ni hooks activos). Si lo es: registra `/kiro`, pide `/kiro/status` una vez y guarda el mapa de respaldos. Se guarda en variables del módulo, no en `$.state`: un reload vuelve a lanzar `session.start` y lo rehace, así que no hace falta el contrato de tipos de `$.state`.
- `turn.step`, antes de `next`: `GET /health`; si falla, `kiro-gateway.ps1 start`, toast y sigue. Un intento por paso.
- `turn.step`, después de `next`: si `stopReason === 'refusal'` y el modelo tiene respaldo, un único `next({ ...e, model: fallback })` con toast.

- [ ] **Step 1: Tests (fallan)**

`claude-mod/tests/world.ts`:

```ts
// Mundo simulado bajo el mod para los tests: env, gateway (http.fetch),
// script (process.run), toasts, modelo de la sesión y el modelo (turn.step).
import { mock, type Engine } from 'claude-code/testing'
import type { On } from 'claude-code'
import type { Status } from '../hooks/kiro.ts'

export const STATUS: Status = {
  version: 'v-test',
  uptime_seconds: 125,
  active_account: 'acc1',
  debug: { mode: 'off', dir: 'C:\\kg\\debug_logs' },
  models: [
    { id: 'claude-haiku-4.5', native_thinking: [], effort_levels: [], refusal_fallback: '' },
    { id: 'claude-sonnet-5.5', native_thinking: ['adaptive'], effort_levels: ['low', 'high'], refusal_fallback: 'claude-sonnet-5' },
  ],
}

export type World = {
  healthy: boolean
  statusCode: number
  runExit: number
  refuse: (model: string) => boolean
  fetches: string[]
  runs: string[][]
  toasts: string[]
  registered: string[]
  steps: string[]
  commands: { command: string; args: string }[]
}

const LOCAL_ENV = { ANTHROPIC_BASE_URL: 'http://127.0.0.1:8000', ANTHROPIC_AUTH_TOKEN: 'k-test' }

/** El mundo bajo el mod: env, gateway (fetch), script (process.run), toasts y modelo. */
export function world(on: On, opts: Partial<World> & { env?: Record<string, string> } = {}): World {
  const w: World = {
    healthy: true, statusCode: 200, runExit: 0, refuse: m => m === 'claude-sonnet-5-5',
    fetches: [], runs: [], toasts: [], registered: [], steps: [], commands: [], ...opts,
  }
  mock.env(on, opts.env ?? LOCAL_ENV)
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('command.register', ($, e) => { w.registered.push(e.name); return { value: { command: e.name } } })
  on('ui.toast', ($, e) => { w.toasts.push(e.text); return { value: undefined } })
  on('session.model', () => ({ value: 'claude-sonnet-5-5' }))
  on('command.run', ($, e) => { w.commands.push({ command: e.command, args: e.args }); return { text: '' } })
  on('http.fetch', ($, e) => {
    w.fetches.push(e.url)
    if (!w.healthy) throw new Error('connect ECONNREFUSED 127.0.0.1:8000')
    if (e.url.endsWith('/health')) return { value: { status: 200, ok: true, headers: {}, text: '{"status":"healthy"}' } }
    if (e.url.endsWith('/kiro/status')) {
      const ok = w.statusCode === 200
      return { value: { status: w.statusCode, ok, headers: {}, text: ok ? JSON.stringify(STATUS) : '{"type":"error"}' } }
    }
    return { value: { status: 404, ok: false, headers: {}, text: '' } }
  })
  on('process.run', ($, e) => {
    w.runs.push([...e.argv])
    if (w.runExit === 0) w.healthy = true
    const stdout = w.runExit === 0 ? 'kiro-gateway listo en http://127.0.0.1:8000' : 'error: No encuentro kiro-gateway.exe'
    return { value: { exitCode: w.runExit, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('turn.step', async function* ($, e) {
    w.steps.push(e.model)
    return { turnId: e.turnId, index: e.index, answer: w.refuse(e.model) ? '' : 'ok', toolUses: [], stopReason: w.refuse(e.model) ? 'refusal' : 'end_turn', usage: null }
  })
  return w
}

export async function start($: Engine) {
  await $.session.start({ cwd: 'C:/repo', surface: null, isInteractive: false })
}
```

`claude-mod/tests/register.test.ts`:

```ts
import { describe, expect, test, type Engine } from 'claude-code/testing'
import { start, world } from './world.ts'

async function step($: Engine, model: string) {
  const s = $.turn.step({ turnId: 't1', index: 0, model, messageCount: 1 })
  for await (const _ of s) { /* drena los chunks */ }
  return s.result
}

describe('arranque', () => {
  test('inerte si la sesión no va por un gateway local', async ($, on) => {
    const w = world(on, { env: { ANTHROPIC_BASE_URL: 'https://api.anthropic.com' } })
    await start($)
    const r = await step($, 'claude-sonnet-5-5')
    expect(w.registered).toEqual([])
    expect(w.fetches).toEqual([])
    expect(w.steps).toEqual(['claude-sonnet-5-5'])
    expect(r.stopReason).toBe('refusal')
  })

  test('registra /kiro y pide el estado una vez', async ($, on) => {
    const w = world(on)
    await start($)
    expect(w.registered).toEqual(['kiro'])
    expect(w.fetches).toEqual(['http://127.0.0.1:8000/kiro/status'])
  })
})

describe('gateway caído', () => {
  test('lo relanza con el script y sigue', async ($, on) => {
    const w = world(on)
    await start($)
    w.healthy = false
    const r = await step($, 'claude-haiku-4-5')
    expect(w.runs).toHaveLength(1)
    expect(w.runs[0]!.slice(0, 4)).toEqual(['pwsh', '-NoProfile', '-NonInteractive', '-File'])
    expect(w.runs[0]![4]).toMatch(/[\\/]\.\.[\\/]scripts[\\/]kiro-gateway\.ps1$/)
    expect(w.runs[0]!.slice(5)).toEqual(['start', '-Port', '8000'])
    expect(w.toasts).toEqual(['kiro-gateway relanzado'])
    expect(w.steps).toEqual(['claude-haiku-4-5'])
    expect(r.stopReason).toBe('end_turn')
  })

  test('si no arranca avisa una vez y deja seguir la petición', async ($, on) => {
    const w = world(on, { runExit: 1 })
    await start($)
    w.healthy = false
    await step($, 'claude-haiku-4-5')
    expect(w.runs).toHaveLength(1)
    expect(w.toasts).toEqual(['kiro-gateway no arrancó: error: No encuentro kiro-gateway.exe'])
    expect(w.steps).toEqual(['claude-haiku-4-5'])
  })
})

describe('refusal', () => {
  test('reintenta una vez con el respaldo de Kiro (id de Claude Code)', async ($, on) => {
    const w = world(on)
    await start($)
    const r = await step($, 'claude-sonnet-5-5')
    expect(w.steps).toEqual(['claude-sonnet-5-5', 'claude-sonnet-5'])
    expect(w.toasts).toEqual(['claude-sonnet-5-5 cortó → reintento con claude-sonnet-5'])
    expect(r.stopReason).toBe('end_turn')
    expect(r.answer).toBe('ok')
  })

  test('no reintenta dos veces si el respaldo también corta', async ($, on) => {
    const w = world(on, { refuse: () => true })
    await start($)
    const r = await step($, 'claude-sonnet-5-5')
    expect(w.steps).toEqual(['claude-sonnet-5-5', 'claude-sonnet-5'])
    expect(r.stopReason).toBe('refusal')
  })

  test('sin respaldo no reintenta', async ($, on) => {
    const w = world(on, { refuse: () => true })
    await start($)
    await step($, 'claude-haiku-4-5')
    expect(w.steps).toEqual(['claude-haiku-4-5'])
    expect(w.toasts).toEqual([])
  })
})
```

- [ ] **Step 2: Comprobar que fallan**

Run: `claude plugin test claude-mod`
Expected: FAIL en `registra /kiro…` (`w.registered` vacío) y en los de gateway caído y refusal; pasa `inerte…`.

- [ ] **Step 3: Implementar `register.ts`**

`claude-mod/hooks/register.ts`:

```ts
// Mod `kiro`: hace que Claude Code sobre kiro-gateway no se corte.
//  - Un refusal reintenta una vez con el modelo de respaldo que declara Kiro.
//  - Un gateway caído se relanza con scripts/kiro-gateway.ps1.
//  - /kiro controla el gateway (estado, restart, logs, debug, models).
// Fuera de un gateway local (ANTHROPIC_BASE_URL) queda inerte.
import type { EngineInterface, Register } from 'claude-code'
import { fallbackMap, localBase, modelKey, type Status } from './kiro.ts'

export const register: Register = on => {
  // Estado del módulo: un reload vuelve a lanzar session.start y lo rehace.
  const state = { base: '', port: '', token: '', fallbacks: new Map<string, string>() }

  const scriptPath = ($: EngineInterface) =>
    `${$.plugin.root.replace(/[\\/]\.claude-plugin[\\/]?$/, '')}/../scripts/kiro-gateway.ps1`

  async function healthy($: EngineInterface): Promise<boolean> {
    try {
      return (await $.http.fetch(`${state.base}/health`)).ok
    } catch {
      return false
    }
  }

  async function fetchStatus($: EngineInterface): Promise<Status> {
    const r = await $.http.fetch(`${state.base}/kiro/status`, { headers: { Authorization: `Bearer ${state.token}` } })
    if (r.status === 401) throw new Error('401: la key de kclaude no coincide con PROXY_API_KEY del .env del gateway.')
    if (!r.ok) throw new Error(`/kiro/status respondió ${r.status}`)
    const st = JSON.parse(r.text) as Status
    state.fallbacks = fallbackMap(st.models)
    return st
  }

  async function runScript($: EngineInterface, args: string[]): Promise<{ ok: boolean; text: string }> {
    const r = await $.process.run(
      ['pwsh', '-NoProfile', '-NonInteractive', '-File', scriptPath($), ...args, '-Port', state.port],
      { timeoutMs: 60_000 },
    )
    return { ok: r.exitCode === 0, text: `${r.stdout}${r.stderr}`.trim() }
  }

  on('session.start', async ($, e, next) => {
    const started = await next(e)
    const local = localBase(await $.env.get('ANTHROPIC_BASE_URL'))
    if (!local) return started
    state.base = local.base
    state.port = local.port
    state.token = (await $.env.get('ANTHROPIC_AUTH_TOKEN')) ?? ''
    await $.command.register({
      name: 'kiro',
      description: 'Estado y control de kiro-gateway',
      argumentHint: '[restart | logs [n] | debug all|errors|off | models [id]]',
    })
    await fetchStatus($).catch(() => undefined) // sin estado: /kiro lo dirá
    return started
  })

  on('turn.step', async function* ($, e, next) {
    if (!state.base) return yield* next(e)

    if (!(await healthy($))) {
      const r = await runScript($, ['start'])
      $.ui.toast(r.ok ? 'kiro-gateway relanzado' : `kiro-gateway no arrancó: ${r.text}`, { timeoutMs: 8000 })
      if (r.ok) await fetchStatus($).catch(() => undefined)
    }

    const first = yield* next(e)
    const fallback = state.fallbacks.get(modelKey(e.model))
    if (first.stopReason !== 'refusal' || !fallback) return first
    $.ui.toast(`${e.model} cortó → reintento con ${fallback}`, { timeoutMs: 8000 })
    return yield* next({ ...e, model: fallback })
  })
}
```

- [ ] **Step 4: Comprobar que pasan, validar y tipar**

Run: `claude plugin test claude-mod && claude plugin validate claude-mod && tsc -p claude-mod`
Expected: PASS; el validador lista `ANTHROPIC_BASE_URL` y `ANTHROPIC_AUTH_TOKEN` como variables leídas y los hooks `session.start` y `turn.step`; `tsc` sin errores.

- [ ] **Step 5: Commit**

```bash
git add claude-mod
git commit -m "feat(mod): relanza el gateway caído y reintenta un refusal con el respaldo de Kiro

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: El comando `/kiro`

**Files:**
- Modify: `claude-mod/hooks/register.ts`
- Create: `claude-mod/tests/command.test.ts`

**Interfaces:**
- Consumes: `state`, `healthy`, `fetchStatus`, `runScript` (Task 5); `formatStatus`, `formatModels`, `parseArgs`, `modelKey`, `USAGE` (Task 4); `world`, `start` de `tests/world.ts` (Task 5).

| Uso | Hace |
| --- | --- |
| `/kiro` | `formatStatus(status, $.session.model())` |
| `/kiro restart` | `restart -DebugMode <modo actual>` (sin `-DebugMode` si el gateway no responde); refresca el estado |
| `/kiro logs [n]` | `logs -Lines n` (20 por defecto); `n` entero > 0 o uso |
| `/kiro debug all\|errors\|off` | `restart -DebugMode <modo>`; muestra modo y carpeta |
| `/kiro models` | `formatModels` |
| `/kiro models <id>` | Si Kiro lo conoce, lanza `/model <id>` con `$.command.run` sin esperar |
| Otro | `USAGE` |

Errores: si algo falla y `/health` no responde → «kiro-gateway no responde en <base>. Prueba /kiro restart.»; si no, el mensaje del error (el de 401 lo da `fetchStatus`).

- [ ] **Step 1: Tests (fallan)**

`claude-mod/tests/command.test.ts`:

```ts
import { describe, expect, test, type Engine } from 'claude-code/testing'
import { start, world } from './world.ts'

async function kiro($: Engine, args = '') {
  return (await $.command.run({ command: 'kiro', args })).text ?? ''
}

describe('/kiro', () => {
  test('sin argumentos muestra el estado', async ($, on) => {
    world(on)
    await start($)
    const text = await kiro($)
    expect(text).toContain('kiro-gateway v-test')
    expect(text).toContain('cuenta: acc1')
    expect(text).toContain('debug: off')
    expect(text).toContain('modelo de la sesión: claude-sonnet-5-5')
  })

  test('restart conserva el DEBUG_MODE actual', async ($, on) => {
    const w = world(on)
    await start($)
    const text = await kiro($, 'restart')
    expect(w.runs.map(r => r.slice(5))).toEqual([['restart', '-DebugMode', 'off', '-Port', '8000']])
    expect(text).toBe('kiro-gateway listo en http://127.0.0.1:8000')
  })

  test('logs pasa n y valida el número', async ($, on) => {
    const w = world(on)
    await start($)
    await kiro($, 'logs')
    await kiro($, 'logs 50')
    expect(w.runs.map(r => r.slice(5))).toEqual([
      ['logs', '-Lines', '20', '-Port', '8000'],
      ['logs', '-Lines', '50', '-Port', '8000'],
    ])
    expect(await kiro($, 'logs x')).toBe('Uso: /kiro logs [n]')
  })

  test('debug reinicia con el modo pedido y valida', async ($, on) => {
    const w = world(on)
    await start($)
    const text = await kiro($, 'debug all')
    expect(w.runs.map(r => r.slice(5))).toEqual([['restart', '-DebugMode', 'all', '-Port', '8000']])
    expect(text).toContain('C:\\kg\\debug_logs')
    expect(await kiro($, 'debug mucho')).toBe('Uso: /kiro debug all|errors|off')
  })

  test('models muestra la tabla y valida el id', async ($, on) => {
    world(on)
    await start($)
    expect(await kiro($, 'models')).toContain('claude-sonnet-5.5  adaptive')
    expect(await kiro($, 'models gpt-9')).toBe('gpt-9 no está en la lista de Kiro. /kiro models para verla.')
    expect(await kiro($, 'models claude-sonnet-5-5')).toBe('Cambiando el modelo de la sesión a claude-sonnet-5-5…')
  })

  test('subcomando desconocido muestra el uso', async ($, on) => {
    world(on)
    await start($)
    expect(await kiro($, 'hola')).toBe('Uso: /kiro [restart | logs [n] | debug all|errors|off | models [id]]')
  })

  test('key que no coincide: lo dice', async ($, on) => {
    world(on, { statusCode: 401 })
    await start($)
    expect(await kiro($)).toBe('401: la key de kclaude no coincide con PROXY_API_KEY del .env del gateway.')
  })

  test('gateway apagado: sugiere /kiro restart', async ($, on) => {
    const w = world(on)
    await start($)
    w.healthy = false
    expect(await kiro($)).toBe('kiro-gateway no responde en http://127.0.0.1:8000. Prueba /kiro restart.')
  })
})
```

- [ ] **Step 2: Comprobar que fallan**

Run: `claude plugin test claude-mod`
Expected: FAIL en los tests de `/kiro` (ningún hook responde al comando `kiro`).

- [ ] **Step 3: Implementar el comando**

En `claude-mod/hooks/register.ts`, amplía el import de `./kiro.ts`:

```ts
import { USAGE, fallbackMap, formatModels, formatStatus, localBase, modelKey, parseArgs, type Status } from './kiro.ts'
```

Añade, encima de `export const register`:

```ts
const DEBUG_MODES = ['all', 'errors', 'off']
```

Y dentro de `register`, después del hook de `turn.step`:

```ts
  on('command.run', { command: 'kiro' }, async ($, e) => {
    if (!state.base) return { text: 'El mod kiro solo actúa con ANTHROPIC_BASE_URL apuntando a un gateway local (kclaude).' }
    const { sub, rest } = parseArgs(e.args)
    try {
      switch (sub) {
        case '':
          return { text: formatStatus(await fetchStatus($), await $.session.model()) }
        case 'restart': {
          const st = await fetchStatus($).catch(() => undefined)
          const r = await runScript($, ['restart', ...(st ? ['-DebugMode', st.debug.mode] : [])])
          if (r.ok) await fetchStatus($).catch(() => undefined)
          return { text: r.text }
        }
        case 'logs': {
          const n = rest[0] === undefined ? 20 : Number(rest[0])
          if (!Number.isInteger(n) || n <= 0) return { text: 'Uso: /kiro logs [n]' }
          return { text: (await runScript($, ['logs', '-Lines', String(n)])).text }
        }
        case 'debug': {
          const mode = rest[0] ?? ''
          if (!DEBUG_MODES.includes(mode)) return { text: 'Uso: /kiro debug all|errors|off' }
          const r = await runScript($, ['restart', '-DebugMode', mode])
          if (!r.ok) return { text: r.text }
          const st = await fetchStatus($)
          return { text: `${r.text}\ndebug: ${st.debug.mode} → ${st.debug.dir}` }
        }
        case 'models': {
          const st = await fetchStatus($)
          const id = rest[0]
          if (!id) return { text: formatModels(st.models) }
          if (!st.models.some(m => modelKey(m.id) === modelKey(id))) {
            return { text: `${id} no está en la lista de Kiro. /kiro models para verla.` }
          }
          // Sin await: /model se encola y corre cuando esta orden termina.
          $.command.run({ command: 'model', args: id }).catch(err => $.ui.toast(`/model ${id} falló: ${String(err)}`))
          return { text: `Cambiando el modelo de la sesión a ${id}…` }
        }
        default:
          return { text: USAGE }
      }
    } catch (err) {
      if (!(await healthy($))) return { text: `kiro-gateway no responde en ${state.base}. Prueba /kiro restart.` }
      return { text: err instanceof Error ? err.message : String(err) }
    }
  })
```

- [ ] **Step 4: Comprobar que pasan, validar y tipar**

Run: `claude plugin test claude-mod && claude plugin validate claude-mod && tsc -p claude-mod`
Expected: PASS (los de Task 4, 5 y 6), validación limpia y `tsc` sin errores.

- [ ] **Step 5: Commit**

```bash
git add claude-mod
git commit -m "feat(mod): comando /kiro (estado, restart, logs, debug, models)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: E2E del reintento con el motor real (gateway falso)

Prueba con Claude Code de verdad que el reintento deja la respuesta bien formada. No toca Kiro: un gateway falso responde `refusal` para `claude-sonnet-5-5` y `ok` para lo demás. Decide si se queda el reintento automático o se aplica el plan B de la spec.

**Files:**
- Create: `claude-mod/e2e/fake_gateway.py`

- [ ] **Step 1: Gateway falso**

`claude-mod/e2e/fake_gateway.py`:

```python
"""Gateway falso para el e2e del mod kiro: /health, /kiro/status y
/v1/messages en SSE. claude-sonnet-5-5 corta con stop_reason "refusal"; el
resto responde "ok". Registra en el log el modelo de cada /v1/messages.

Uso: uv run --no-project python fake_gateway.py <puerto> <log>
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1])
LOG = open(sys.argv[2], "a", buffering=1, encoding="utf-8")
REFUSE = "claude-sonnet-5-5"
STATUS = {
    "version": "e2e", "uptime_seconds": 1.0, "active_account": "e2e",
    "debug": {"mode": "off", "dir": "C:\\e2e\\debug_logs"},
    "models": [
        {"id": "claude-sonnet-5.5", "native_thinking": ["adaptive"], "effort_levels": [], "refusal_fallback": "claude-sonnet-5"},
        {"id": "claude-sonnet-5", "native_thinking": ["adaptive"], "effort_levels": [], "refusal_fallback": ""},
    ],
}


def sse(event, data):
    return f"event: {event}\ndata: {json.dumps(data)}\n\n".encode()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def send_json(self, code, body):
        raw = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        if self.path == "/health":
            return self.send_json(200, {"status": "healthy"})
        if self.path == "/kiro/status":
            return self.send_json(200, STATUS)
        self.send_json(404, {})

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("content-length", 0))) or b"{}")
        if not self.path.startswith("/v1/messages") or "count_tokens" in self.path:
            return self.send_json(404, {})
        model = body.get("model", "")
        LOG.write(f"model={model}\n")
        refuse = model.lower().replace(".", "-") == REFUSE
        self.send_response(200)
        self.send_header("content-type", "text/event-stream")
        self.end_headers()
        msg = {"id": "msg_e2e", "type": "message", "role": "assistant", "model": model, "content": [],
               "stop_reason": None, "stop_sequence": None, "usage": {"input_tokens": 5, "output_tokens": 0}}
        out = sse("message_start", {"type": "message_start", "message": msg})
        if not refuse:
            out += sse("content_block_start", {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}})
            out += sse("content_block_delta", {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "ok"}})
            out += sse("content_block_stop", {"type": "content_block_stop", "index": 0})
        out += sse("message_delta", {"type": "message_delta",
                                     "delta": {"stop_reason": "refusal" if refuse else "end_turn", "stop_sequence": None},
                                     "usage": {"output_tokens": 1}})
        out += sse("message_stop", {"type": "message_stop"})
        self.wfile.write(out)


ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
```

- [ ] **Step 2: Ejecutar el e2e**

```bash
cd claude-mod/e2e
rm -f requests.log result.json
uv run --no-project python fake_gateway.py 8996 requests.log &
sleep 2
ANTHROPIC_BASE_URL=http://127.0.0.1:8996 ANTHROPIC_AUTH_TOKEN=e2e-key ANTHROPIC_API_KEY= CLAUDE_CODE_ATTRIBUTION_HEADER=0 \
  claude -p "hola" --model claude-sonnet-5-5 --plugin-dir "$(cd .. && pwd)" --output-format json > result.json
echo "exit=$?"; cat requests.log
uv run --no-project python -c "import json; r=json.load(open('result.json')); print(r['is_error'], repr(r['result']), r['stop_reason'])"
pwsh -NoProfile -Command "Stop-Process -Id (Get-NetTCPConnection -LocalPort 8996 -State Listen).OwningProcess -Force"
cd ../..
```

Expected: `exit=0`; `requests.log` con `model=claude-sonnet-5-5` y después `model=claude-sonnet-5` (puede haber un `claude-sonnet-5-5` más antes si el reintento propio de CC con el mismo modelo corre por debajo del hook; anotarlo); la última línea `False 'ok' end_turn`.

- [ ] **Step 3: Decidir**

- **Pasa** (`False 'ok' end_turn` y el respaldo aparece en el log): se queda el reintento automático. Anota en `claude-mod/e2e/fake_gateway.py`, en el docstring, el número de peticiones que hizo CC por un refusal (2 o 3), porque es lo que cuesta un corte en la cuenta de Kiro.
- **Falla** porque la respuesta queda mal formada (`is_error` `True`, o `result` que no es `'ok'` aunque el log muestre la petición a `claude-sonnet-5`): aplica el plan B de la spec. En `register.ts`:
  1. Añade al `state` el campo `pending: undefined as { fallback: string } | undefined`.
  2. En `turn.step`, sustituye las tres últimas líneas (desde `$.ui.toast(\`${e.model} cortó…`) por:

     ```ts
     state.pending = { fallback }
     $.ui.toast(`${e.model} cortó. /kiro retry reintenta con ${fallback}`, { timeoutMs: 10000 })
     return first
     ```

  3. En el `switch` de `/kiro`, antes de `default`:

     ```ts
        case 'retry': {
          if (!state.pending) return { text: 'No hay ningún corte que reintentar.' }
          const { fallback } = state.pending
          state.pending = undefined
          const last = (await $.session.messages()).filter(m => m.role === 'user').at(-1)
          if (!last?.text) return { text: 'No encuentro el último prompt para reenviarlo.' }
          $.command.run({ command: 'model', args: fallback })
            .then(() => $.prompt.submit({ text: last.text }))
            .catch(err => $.ui.toast(`/kiro retry falló: ${String(err)}`))
          return { text: `Reintento con ${fallback}…` }
        }
     ```

  4. Cambia los tests de refusal de `register.test.ts`: `reintenta una vez…` pasa a esperar `w.steps` = `['claude-sonnet-5-5']` y el toast `'claude-sonnet-5-5 cortó. /kiro retry reintenta con claude-sonnet-5'`; `no reintenta dos veces…` se borra. Añade `'retry'` a `USAGE` y al `argumentHint`.
  5. Repite `claude plugin test claude-mod && claude plugin validate claude-mod && tsc -p claude-mod` y vuelve a correr el Step 2 esperando solo `model=claude-sonnet-5-5` en el log.

- [ ] **Step 4: Commit**

```bash
git add claude-mod .gitignore
git commit -m "test(mod): e2e del reintento por refusal con el motor real y un gateway falso

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Verificación en vivo con `kclaude` y cierre

Usa el gateway y la cuenta reales. Coste en Kiro: 2 prompts cortos («di hola»). No se fuerza ningún refusal.

- [ ] **Step 1: Suite completa**

Run: `go test ./... && go vet ./... && claude plugin test claude-mod && claude plugin validate claude-mod && tsc -p claude-mod && task build`
Expected: todo en verde y binario recompilado.

- [ ] **Step 2: `/kiro` en una sesión real**

```bash
pwsh -NoProfile -File scripts/kiro-gateway.ps1 stop
pwsh -NoProfile -File scripts/kiro-claude.ps1 -p "/kiro"
pwsh -NoProfile -File scripts/kiro-claude.ps1 -p "/kiro models"
pwsh -NoProfile -File scripts/kiro-claude.ps1 -p "/kiro logs 3"
```

Expected: kclaude arranca el gateway (no estaba corriendo); `/kiro` muestra versión, cuenta, `debug: off (…\debug_logs)` y el modelo; `/kiro models` muestra la tabla con `claude-sonnet-5.5 … claude-sonnet-5`; `/kiro logs 3` las cabeceras `== …gateway.log` y `== …gateway.err.log`. Ninguna de estas hace peticiones de chat a Kiro.

- [ ] **Step 3: Relanzado automático**

```bash
pwsh -NoProfile -File scripts/kiro-gateway.ps1 stop
ANTHROPIC_BASE_URL=http://127.0.0.1:8000 ANTHROPIC_AUTH_TOKEN="$(grep -E '^\s*PROXY_API_KEY\s*=' .env | sed -E 's/^\s*PROXY_API_KEY\s*=\s*//; s/["'"'"']//g' | tr -d '\r')" ANTHROPIC_API_KEY= CLAUDE_CODE_ATTRIBUTION_HEADER=0 \
  claude -p "di hola" --model claude-haiku-4-5 --plugin-dir "$(pwd)/claude-mod" --output-format json | uv run --no-project python -c "import json,sys; r=json.load(sys.stdin); print(r['is_error'], r['result'][:80])"
curl -s http://127.0.0.1:8000/health
```

(Se llama a `claude` directamente porque kclaude arrancaría el gateway antes que el mod; así es el mod el que lo relanza.)

Expected: `False` y un saludo; `/health` responde. El gateway lo relanzó el hook de `turn.step`.

- [ ] **Step 4: `/kiro restart`, `/kiro debug` y `/kiro models <id>` en interactivo**

Pide al usuario que abra `kclaude` y ejecute, en este orden: `/kiro debug errors`, `/kiro` (debe decir `debug: errors`), `/kiro restart`, `/kiro` (sigue `errors`), `/kiro debug off`, `/kiro models claude-haiku-4.5` y luego `/model` para ver que cambió, y un prompt corto («di hola»). Si `/kiro models <id>` no cambia el modelo, cambia ese `case` para que devuelva `{ text: \`Usa /model ${id} para cambiar el modelo de la sesión.\` }`, ajusta su test y la fila del README, y vuelve a pasar el Step 1.

- [ ] **Step 5: Commit de ajustes (si los hubo) y memoria**

```bash
git status --short
git add -A claude-mod scripts README.md docs
git commit -m "fix(mod): ajustes tras la verificación en vivo

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

(Solo si hubo cambios.) Actualiza la memoria `spike-claude-code-integration.md`: nivel 3 implementado en `feat/claude-mod`, resultado del e2e (peticiones por refusal, plan A o B) y de la verificación en vivo; pendiente el merge, que decide el usuario.
