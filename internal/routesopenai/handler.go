// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package routesopenai implementa los endpoints HTTP compatibles con OpenAI
// (`GET /v1/models`, `POST /v1/chat/completions`). Port de
// .upstream/kiro/routes_openai.py:1-769, verificado línea a línea contra el
// original y contra el documento de diseño
// (docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md, §7.1).
//
// Este paquete NO valida la API key (`Authorization: Bearer`): eso lo hace
// el middleware de internal/server (Task 11, ver
// .superpowers/sdd/2026-09-16-fase-5-streaming-y-rutas/task-11-brief.md), que
// monta Handler.Models y Handler.ChatCompletions detrás del chequeo. Tampoco
// captura panics: converterscore expone tres panic() deliberados (fase 3,
// ValueError sin equivalente en la firma Go) que se propagan tal cual hasta
// el middleware de recuperación de Task 11.
//
// # Desviaciones documentadas frente al brief/upstream
//
//  1. owned_by en /v1/models. El brief de esta tarea (plan de fase 5, no el
//     spec) describe `"owned_by":"kiro"`. El upstream real
//     (routes_openai.py:151) fija literalmente `owned_by="anthropic"` — y la
//     instrucción de esta tarea es que upstream gana sobre la prosa del
//     brief en caso de conflicto. Esta implementación usa "anthropic", igual
//     que el original, e incluye también `description="Claude model via
//     Kiro API"` (routes_openai.py:152), que el brief omitía.
//  2. Dialecto de error unificado. El spec (§7.1) documenta un único sobre
//     de error OpenAI, `{"error":{"message","type":"kiro_api_error","code"}}`,
//     como "formato de error por dialecto" para esta ruta. El upstream real
//     es más inconsistente: usa ese sobre solo para las respuestas
//     JSONResponse explícitas (Kiro no-2xx), pero dedja que FastAPI's default
//     exception handler para HTTPException devuelva `{"detail": "..."}` en
//     las rutas de agotamiento de cuentas (raise HTTPException(503, ...)).
//     Esta implementación usa el sobre unificado del spec para TODAS las
//     respuestas de error de /v1/chat/completions (incluida la de cuentas
//     agotadas) — más consistente para un cliente OpenAI, y es lo que
//     test-scenario 5 del brief ("503 con formato OpenAI error") describe.
//  3. Kiro siempre en modo streaming. routes_openai.py:358-363 y :612-617
//     pasan `stream=True` a `http_client.request_with_retry` de forma
//     INCONDICIONAL, tanto si `request_data.stream` es true como false: la
//     preferencia de streaming del cliente OpenAI solo decide CÓMO
//     formateamos la respuesta (SSE vs JSON único), nunca cómo hablamos con
//     Kiro. Este port replica eso: `attemptAccount` (failover.go) llama
//     `h.client.RequestWithRetry(ctx, httpReq, acc.Auth, true)` con el
//     último parámetro fijo a `true`, no `req.Stream`.
//  4. Fallos de transporte (red) siempre Recoverable. routes_openai.py:512-517
//     fuerza explícitamente ErrorType.RECOVERABLE para errores de TRANSPORTE
//     (timeouts, DNS, conexión rechazada, reintentos agotados...) —
//     *httpclient.RequestError en este port, distinto de una respuesta HTTP
//     no-2xx real de Kiro. handleTransportError (failover.go) replica eso
//     llamando a accountmanager.Manager.ReportFailureAs con
//     accounterrors.Recoverable forzado, en vez de dejar que
//     accounterrors.Classify(statusCode, reason) lo reclasifique como Fatal
//     (la tabla trata cualquier 5xx como Fatal, correcto para una respuesta
//     HTTP real, no para un fallo de transporte) — ver el comentario de
//     ReportFailureAs en internal/accountmanager/failover.go.
package routesopenai

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/accountmanager"
	"github.com/marr-cloud/kiro-gateway-go/internal/cache"
	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/httpclient"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelresolver"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
	"github.com/marr-cloud/kiro-gateway-go/internal/streamingopenai"
	"github.com/marr-cloud/kiro-gateway-go/internal/thinkingparser"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationstate"
)

// modelDescription es la descripción literal que routes_openai.py:152 pone
// en cada modelo de /v1/models.
const modelDescription = "Claude model via Kiro API"

// Handler implementa /v1/models y /v1/chat/completions.
type Handler struct {
	accounts *accountmanager.Manager
	client   *httpclient.Client
	cfg      *config.Config

	// truncation es la cache compartida de recuperación de truncación
	// (Task 8a/8b): la MISMA instancia que routesanthropic.Handler.truncation,
	// inyectada por internal/server.New (Task 11 fix). El lado SAVE (Task
	// 8b, tras cerrar un stream truncado) escribe vía SetTool/SetContent; el
	// lado READ/inject de este paquete (truncationinject.go) lee vía
	// GetTool/GetContent al recibir la SIGUIENTE petición — ver
	// routes_openai.py:185-234.
	truncation *truncationstate.State

	// apiURL construye la URL de destino en Kiro para una cuenta dada. Por
	// defecto, acc.Auth.APIHost()+"/generateAssistantResponse"
	// (producción, routes_openai.py:347). auth.Manager.APIHost() siempre
	// devuelve un host real bajo *.kiro.dev — no hay override público a
	// propósito, no es un seam de producción (ver
	// internal/auth/manager.go:36-47) — así que handler_test.go (mismo
	// paquete, sin sufijo `_test` externo) sobreescribe este campo no
	// exportado para apuntar a su httptest.Server. Mismo patrón que
	// accountmanager.Manager.listURLOverride/isRuntimeEndpointOverride
	// (internal/accountmanager/manager.go:45-54) usa para el mismo
	// problema. Un *Handler construido por New() fuera de este paquete
	// nunca puede leer ni tocar este campo.
	apiURL func(acc *accountmanager.Account) string
}

// New construye un Handler. accounts y client deben estar ya inicializados
// (accounts.LoadCredentials/Initialize ya corridos; client listo para
// RequestWithRetry) — New no hace I/O por sí mismo. truncation es la cache
// compartida de recuperación de truncación (Task 8a/8b) — el llamador
// (internal/server.New) debe pasar la MISMA instancia que routesanthropic.New
// recibe, para que el save de una petición y el inject de la siguiente vean
// el mismo estado.
func New(accounts *accountmanager.Manager, client *httpclient.Client, cfg *config.Config, truncation *truncationstate.State) *Handler {
	return &Handler{
		accounts:   accounts,
		client:     client,
		cfg:        cfg,
		truncation: truncation,
		apiURL: func(acc *accountmanager.Account) string {
			return acc.Auth.APIHost() + "/generateAssistantResponse"
		},
	}
}

// Models responde GET /v1/models con la unión de modelos disponibles de
// todas las cuentas (accountmanager.Manager.GetAllAvailableModels(), ya
// ordenada). Port de routes_openai.py:122-157.
func (h *Handler) Models(w http.ResponseWriter, r *http.Request) {
	// Aplica el catálogo del modelresolver (HIDDEN_FROM_LIST + aliases) sobre
	// la unión de modelos de las cuentas, como get_available_models
	// (.upstream/kiro/model_resolver.py:370-397): oculta "auto" del listado y
	// muestra el alias "auto-kiro". Cache efímero poblado con los modelos de
	// las cuentas — contra el endpoint runtime.*.kiro.dev no hay descubrimiento
	// dinámico (§6.12), así que la unión de las cuentas ES la lista de modelos
	// disponibles.
	accountIDs := h.accounts.GetAllAvailableModels()
	mc := cache.New(h.cfg.AccountCacheTTL)
	entries := make([]map[string]any, len(accountIDs))
	for i, id := range accountIDs {
		entries[i] = map[string]any{"modelId": id}
	}
	mc.Update(entries)
	resolver := modelresolver.NewModelResolver(mc, modelresolver.HiddenModels, modelresolver.Aliases, modelresolver.HiddenFromList)
	ids := resolver.GetAvailableModels()

	created := time.Now().Unix()

	data := make([]modelsopenai.OpenAIModel, len(ids))
	for i, id := range ids {
		description := modelDescription
		data[i] = modelsopenai.OpenAIModel{
			ID:          id,
			Object:      "model",
			Created:     created,
			OwnedBy:     "anthropic",
			Description: &description,
		}
	}

	writeJSON(w, http.StatusOK, modelsopenai.ModelList{Object: "list", Data: data})
}

// thinkingHandling traduce FAKE_REASONING_HANDLING (cfg) al enum tipado que
// streamingopenai.New espera. thinkingparser.HandlingAsReasoningContent es
// el único de los cuatro valores válidos de la config que streamingopenai
// distingue de "as_content" (formatter.go: AsReasoningContent vs AsContent);
// los otros tres ("remove", "pass", "strip_tags") ya resuelven el contenido
// de thinking ANTES de que streamingcore.Pipeline emita el evento (o lo
// suprimen del todo), así que a este nivel se comportan igual que AsContent.
func (h *Handler) thinkingHandling() streamingopenai.ThinkingHandling {
	if h.cfg.FakeReasoningHandling == thinkingparser.HandlingAsReasoningContent {
		return streamingopenai.AsReasoningContent
	}
	return streamingopenai.AsContent
}

// writeJSON serializa v como JSON con el status dado.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// openAIErrorEnvelope es el sobre de error unificado del spec (§7.1):
// {"error": {"message": "...", "type": "kiro_api_error", "code": <status>}}.
type openAIErrorEnvelope struct {
	Error openAIErrorDetail `json:"error"`
}

type openAIErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    int    `json:"code"`
}

// writeOpenAIError escribe un error en el dialecto OpenAI unificado (ver el
// punto 2 del comentario de cabecera del paquete).
func writeOpenAIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, openAIErrorEnvelope{
		Error: openAIErrorDetail{Message: message, Type: "kiro_api_error", Code: status},
	})
}
