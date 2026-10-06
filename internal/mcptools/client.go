// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package mcptools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/debugmiddleware"
	"github.com/marr-cloud/kiro-gateway-go/internal/pyjson"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// mcpTimeout es el timeout fijo de la llamada MCP, mcp_tools.py:160
// (httpx.AsyncClient(timeout=60.0)). No es configuración — el original
// tampoco lo expone vía variable de entorno.
const mcpTimeout = 60 * time.Second

// Elección de cliente HTTP: net/http directo, NO internal/httpclient.
//
// mcp_tools.py:call_kiro_mcp_api NO pasa por el KiroHttpClient compartido
// (http_client.py, el que backend usan account_manager.py y las rutas de
// streaming): abre un httpx.AsyncClient efímero con timeout=60.0 y hace UN
// solo POST, sin reintentos — ni ante 403 (no hay force-refresh), ni ante
// 429/5xx (un solo intento, mcp_tools.py:163-165,191-202 solo loguean y
// devuelven (None, None)). internal/httpclient.Client.RequestWithRetry SÍ
// implementa 403→refresh y 429/5xx→backoff×3 (client.go de ese paquete),
// que es semántica DISTINTA a la de este endpoint en el original — usarlo
// aquí introduciría reintentos que mcp_tools.py nunca tuvo. Por eso este
// archivo construye un *http.Client{Timeout: mcpTimeout} nuevo en cada
// llamada (igual que "async with httpx.AsyncClient(...) as client" crea un
// cliente nuevo por invocación), en vez de reusar internal/httpclient.
//
// Endpoint y cabeceras: DIVERGENCIA DELIBERADA del original (§D1).
//
// mcp_tools.py hace POST {q_host}/mcp con 3 cabeceras inline (Content-Type
// application/json, Authorization, optout "false") y sin profileArn. Kiro ya
// no acepta esa forma: responde 400 "profileArn is required for this
// request." y, con profileArn pero con el User-Agent por defecto de Go, 403
// "User is not authorized to make this call." (verificado contra
// runtime.us-east-1.kiro.dev el 2026-10-06). Se replica en su lugar lo que
// hace kiro-cli 2.27.1 (capturado con KIRO_LOG_LEVEL=trace): la operación
// RPC AmazonCodeWhispererStreamingService.InvokeMCP — POST {q_host}/ con
// x-amz-target, Content-Type application/x-amz-json-1.0, cabecera
// x-amzn-kiro-profile-arn y "profileArn" en el nivel superior del cuerpo
// JSON-RPC. Las cabeceras parten de utils.GetKiroHeaders (UA de SDK de AWS,
// el mismo que ya acepta GenerateAssistantResponse) y se sobrescriben
// x-amz-target y optout ("false", como mandan tanto el original como
// kiro-cli).
const invokeMCPTarget = "AmazonCodeWhispererStreamingService.InvokeMCP"

// mcpRequestEnvelope es el cuerpo JSON-RPC 2.0 que CallKiroMCPAPI manda a
// InvokeMCP. Puerto de mcp_tools.py:128-137 más ProfileArn (ver arriba).
type mcpRequestEnvelope struct {
	ID         string           `json:"id"`
	JSONRPC    string           `json:"jsonrpc"`
	Method     string           `json:"method"`
	Params     mcpRequestParams `json:"params"`
	ProfileArn string           `json:"profileArn,omitempty"`
}

type mcpRequestParams struct {
	Name      string              `json:"name"`
	Arguments mcpRequestArguments `json:"arguments"`
}

type mcpRequestArguments struct {
	Query string `json:"query"`
}

// mcpResponseEnvelope es la forma mínima que CallKiroMCPAPI necesita leer de
// la respuesta JSON-RPC. Puerto de mcp_tools.py:106-119. Error y
// Result.Content se dejan como json.RawMessage porque hace falta distinguir
// "clave ausente/null" de "clave presente" (mcp_tools.py:180 para Error;
// mcp_tools.py:185 — ver extractMCPResultText — para Content), algo que un
// slice/bool tipado de Go no puede expresar: tanto la ausencia de la clave
// como un valor vacío decodifican al mismo zero value.
type mcpResponseEnvelope struct {
	Result *mcpResult      `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type mcpResult struct {
	Content json.RawMessage `json:"content"`
	IsError bool            `json:"isError"`
}

// isJSONAbsentOrNull reporta si raw representa una clave ausente (slice
// nil/vacío, json.RawMessage nunca escrito por encoding/json) o el literal
// JSON `null` — los dos casos en los que Python `dict.get(key, default)`
// devolvería default. Un valor PRESENTE aunque sea "falsy" (`[]`, `""`, `0`,
// `false`) no cuenta como ausente: `.get` solo mira si la CLAVE existe.
func isJSONAbsentOrNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// extractMCPResultText replica, paso a paso, mcp_tools.py:185:
//
//	result.get("content", [{}])[0].get("text", "{}")
//
// El default de cada .get() SOLO se aplica cuando la clave correspondiente
// FALTA, nunca cuando está presente con un valor vacío — una distinción que
// el port original (fix round 1) colapsaba al usar un slice/string tipado.
// Casos, calcados del original:
//
//   - result nulo, o "content" ausente/null dentro de result: el original
//     sintetiza el default [{}] → índice [0] da {} → .get("text","{}") en
//     {} da "{}" → (ok=true, "{}").
//   - "content" presente como lista NO vacía: se toma el elemento [0]. Si
//     ese elemento no trae la clave "text", aplica su propio default "{}"
//     → (ok=true, "{}"). Si trae "text" (aunque sea ""), se devuelve ese
//     valor TAL CUAL, sin default — un "" hará que el siguiente
//     json.Unmarshal falle (igual que json.loads("") lanza JSONDecodeError
//     en el original), que CallKiroMCPAPI ya trata como fallo.
//   - "content" presente como lista VACÍA ([]): indexar [0] es un
//     IndexError en Python — sin excepción específica que lo capture,
//     propaga al `except Exception` genérico de mcp_tools.py:200-202, que
//     devuelve (None, None). Aquí: ok=false.
func extractMCPResultText(result *mcpResult) (string, bool) {
	if result == nil || isJSONAbsentOrNull(result.Content) {
		return "{}", true
	}

	var items []json.RawMessage
	if err := json.Unmarshal(result.Content, &items); err != nil {
		return "", false
	}
	if len(items) == 0 {
		// content:[] presente → IndexError en el original.
		return "", false
	}

	var item struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(items[0], &item); err != nil {
		return "", false
	}
	if item.Text == nil {
		// La clave "text" falta en el primer elemento → default "{}".
		return "{}", true
	}
	return *item.Text, true
}

// CallKiroMCPAPI llama a la API MCP de Kiro para ejecutar la tool
// web_search. Puerto de mcp_tools.py:77-202 (call_kiro_mcp_api).
//
// host es el q_host del auth_manager (mcp_tools.py:84,157:
// f"{auth_manager.q_host}/mcp") — utils.TokenProvider (utils/headers.go:17)
// no expone el host, así que se recibe explícito en vez de ensanchar esa
// interfaz; el llamador (Task 7) lo obtiene de auth.Manager.QHost().
//
// CRÍTICO (double-deserialization): result.content[0].text es una cadena
// que a su vez contiene JSON (mcp_tools.py:119,184-186) — se deserializa la
// envoltura JSON-RPC y LUEGO esa cadena, por separado.
//
// El original nunca propaga un error al llamador: cualquier fallo (status
// != 200, error JSON-RPC, timeout, red, JSON inválido) se loguea y devuelve
// (None, None) (mcp_tools.py:163-165,180-182,191-202). Este port adapta esa
// forma al idioma de Go devolviendo un error no nil en esos mismos casos —
// mismos puntos de fallo, misma ausencia de reintentos, con la información
// del fallo disponible para quien la quiera loguear en vez de perderse en
// un logger.error() interno.
func CallKiroMCPAPI(ctx context.Context, host, query string, tp utils.TokenProvider) (toolUseID string, results map[string]any, err error) {
	logger := debugmiddleware.FromContext(ctx)

	requestID := NewWebSearchRequestID()
	profileArn := tp.ProfileARN()
	mcpRequest := mcpRequestEnvelope{
		ID:      requestID,
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params: mcpRequestParams{
			Name:      "web_search",
			Arguments: mcpRequestArguments{Query: query},
		},
		ProfileArn: profileArn,
	}
	body, err := json.Marshal(mcpRequest)
	if err != nil {
		return "", nil, fmt.Errorf("mcptools: serializando el request MCP: %w", err)
	}

	// Log del request MCP (mcp_tools.py:139-145). pyjson.Dumps re-emite body
	// con ensure_ascii=False (deshaciendo el escape HTML de <,>,& que mete
	// encoding/json) y conserva el orden de claves; el orden de campos del
	// struct coincide con el dict del original. Un error de Dumps es
	// inalcanzable aquí (body es el JSON que acabamos de serializar) y se
	// ignora en silencio: como el try/except del original (mcp_tools.py:144),
	// el logging nunca debe abortar la llamada MCP.
	if logger != nil {
		if reqDump, derr := pyjson.Dumps(body); derr == nil {
			logger.LogRawChunk([]byte("[MCP REQUEST]\n" + reqDump))
		}
	}

	headers, err := utils.GetKiroHeaders(ctx, tp)
	if err != nil {
		return "", nil, fmt.Errorf("mcptools: obteniendo access token: %w", err)
	}
	// GetKiroHeaders usa claves en minúscula sin canonicalizar (ver su
	// comentario): se sobrescriben por índice directo, no con Set, para no
	// duplicar la cabecera bajo su forma canónica.
	headers["x-amz-target"] = []string{invokeMCPTarget}
	headers["x-amzn-codewhisperer-optout"] = []string{"false"}
	if profileArn != "" {
		headers["x-amzn-kiro-profile-arn"] = []string{profileArn}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/", bytes.NewReader(body))
	if err != nil {
		return "", nil, fmt.Errorf("mcptools: construyendo el request MCP: %w", err)
	}
	req.Header = headers

	client := &http.Client{Timeout: mcpTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("mcptools: llamando a la API MCP: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// mcp_tools.py:163-165: sin reintentar. A diferencia del original se
		// incluye un extracto del body en el error: es lo único que distingue
		// un 400 de validación de un 403 de permisos.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", nil, fmt.Errorf("mcptools: la API MCP respondió %d: %s", resp.StatusCode, bytes.TrimSpace(snippet))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("mcptools: leyendo la respuesta MCP: %w", err)
	}

	var envelope mcpResponseEnvelope
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		// mcp_tools.py:197-199 (json.JSONDecodeError de response.json()).
		return "", nil, fmt.Errorf("mcptools: la respuesta MCP no es JSON válido: %w", err)
	}

	// Log de la respuesta MCP (mcp_tools.py:169-175). Re-emite los bytes crudos
	// de la respuesta con ensure_ascii=False, orden conservado. Igual que en el
	// request, un error de Dumps es inalcanzable (respBody se acaba de
	// deserializar con éxito, así que es JSON válido) y se ignora en silencio
	// para no abortar la llamada.
	if logger != nil {
		if respDump, derr := pyjson.Dumps(respBody); derr == nil {
			logger.LogRawChunk([]byte("[MCP RESPONSE]\n" + respDump))
		}
	}

	if !isJSONAbsentOrNull(envelope.Error) {
		// mcp_tools.py:180-182.
		return "", nil, fmt.Errorf("mcptools: la API MCP devolvió un error: %s", envelope.Error)
	}

	// mcp_tools.py:185 — ver extractMCPResultText para el detalle
	// clave-por-clave. ok=false replica el IndexError de un content:[]
	// presente (fix round 1, Minor #2).
	resultText, ok := extractMCPResultText(envelope.Result)
	if !ok {
		return "", nil, fmt.Errorf("mcptools: la respuesta MCP trae content vacío (mcp_tools.py:185, IndexError en el original)")
	}

	// DOBLE DESERIALIZACIÓN: resultText es una cadena que contiene JSON.
	if err := json.Unmarshal([]byte(resultText), &results); err != nil {
		// mcp_tools.py:186 (json.loads) fallando cae en el mismo except
		// JSONDecodeError de arriba en el original.
		return "", nil, fmt.Errorf("mcptools: el texto interno de la respuesta MCP no es JSON válido: %w", err)
	}

	return NewToolUseID(), results, nil
}
