// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
	"github.com/marr-cloud/kiro-gateway-go/internal/tokenizer"
)

// countTokensResponse es la respuesta de /v1/messages/count_tokens:
// {"input_tokens": <int>} (routes_anthropic.py:958).
type countTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}

// CountTokens responde POST /v1/messages/count_tokens. Endpoint PURAMENTE
// LOCAL: no llama a Kiro ni a GetNextAccount, solo usa internal/tokenizer.
// Port de routes_anthropic.py:909-959.
//
// Replica estimate_request_tokens(apply_claude_correction=True)
// (routes_anthropic.py:948-952), que corrige CADA sub-conteo por separado con
// el factor Claude (1.15) y luego los SUMA — no aplica la corrección una sola
// vez al total. Por eso cada Count* recibe applyCorrection=true y se suman los
// tres resultados (tokenizer.py:296-327). El system prompt llega en su campo
// propio (req.System), a diferencia del dialecto OpenAI.
func (h *Handler) CountTokens(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "error reading request body: "+err.Error())
		return
	}

	var req modelsanthropic.AnthropicCountTokensRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON in request body: "+err.Error())
		return
	}

	inputTokens := tokenizer.CountMessageTokens(marshalMessages(req.Messages), true) +
		tokenizer.CountToolsTokens(marshalTools(req.Tools), true) +
		tokenizer.CountSystemTokens(req.System, true)

	writeJSON(w, http.StatusOK, countTokensResponse{InputTokens: inputTokens})
}
