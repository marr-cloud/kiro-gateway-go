// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/marr-cloud/kiro-gateway-go/internal/accounterrors"
	"github.com/marr-cloud/kiro-gateway-go/internal/accountmanager"
	"github.com/marr-cloud/kiro-gateway-go/internal/convertersanthropic"
	"github.com/marr-cloud/kiro-gateway-go/internal/httpclient"
	"github.com/marr-cloud/kiro-gateway-go/internal/kiroerrors"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
	"github.com/marr-cloud/kiro-gateway-go/internal/networkerrors"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// Messages responde POST /v1/messages. Port de routes_anthropic.py:119-908
// (rama account-system; el modo web_search Path A/B no se porta, ver el punto
// 5 de la cabecera de handler.go).
func (h *Handler) Messages(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "error reading request body: "+err.Error())
		return
	}

	var req modelsanthropic.AnthropicMessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON in request body: "+err.Error())
		return
	}

	h.failoverMessages(r.Context(), w, &req)
}

// failoverMessages es el bucle de failover multi-cuenta, idéntico en forma al
// de routesopenai.failoverChatCompletions: confía en que
// accountmanager.Manager.GetNextAccount agote naturalmente en
// *accountmanager.ExhaustedAccountsError (excluye el conjunto `exclude` de esta
// petición más las cuentas en cuarentena por fallos previos), sin un contador
// MAX_ATTEMPTS explícito (routes_anthropic.py:324,330).
func (h *Handler) failoverMessages(ctx context.Context, w http.ResponseWriter, req *modelsanthropic.AnthropicMessagesRequest) {
	exclude := map[string]struct{}{}
	var lastStatus int
	var lastMessage string
	attempted := false

	for {
		acc, err := h.accounts.GetNextAccount(req.Model, exclude)
		if err != nil {
			h.writeExhausted(w, err, attempted, lastStatus, lastMessage)
			return
		}
		attempted = true

		status, message, done := h.attemptAccount(ctx, w, acc, req)
		if done {
			return
		}

		exclude[acc.ID] = struct{}{}
		lastStatus = status
		lastMessage = message
	}
}

// attemptAccount intenta UNA cuenta. done=true si termina la petición (éxito, o
// error Fatal de Kiro propagado al cliente); done=false con status/mensaje si
// el fallo es Recoverable y el llamador debe probar la siguiente cuenta.
func (h *Handler) attemptAccount(ctx context.Context, w http.ResponseWriter, acc *accountmanager.Account, req *modelsanthropic.AnthropicMessagesRequest) (status int, message string, done bool) {
	conversationID := utils.GenerateConversationID(nil)

	// profileArn obligatorio para runtime.kiro.dev (routes_anthropic.py:380).
	profileArn := acc.Auth.ProfileARN()
	if profileArn == "" {
		profileArn = h.cfg.ProfileARN
	}

	payloadResult := convertersanthropic.AnthropicToKiro(req, conversationID, profileArn)
	payloadBytes, err := json.Marshal(payloadResult.Payload)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "failed to encode Kiro payload: "+err.Error())
		return 0, "", true
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, h.apiURL(acc), bytes.NewReader(payloadBytes))
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "failed to build Kiro request: "+err.Error())
		return 0, "", true
	}

	// Kiro SIEMPRE en modo streaming (punto 2 de la cabecera de handler.go).
	resp, reqErr := h.client.RequestWithRetry(ctx, httpReq, acc.Auth, true)
	if reqErr != nil {
		return h.handleTransportError(acc, req.Model, reqErr)
	}

	if resp.StatusCode != http.StatusOK {
		return h.handleKiroError(w, acc, req.Model, resp)
	}

	h.accounts.ReportSuccess(acc.ID, req.Model)
	if req.Stream {
		h.serveStreaming(w, req, resp)
	} else {
		h.serveNonStreaming(w, req, resp)
	}
	return 0, "", true
}

// handleTransportError trata un fallo de transporte (timeout, DNS, conexión
// rechazada, reintentos agotados) devuelto por RequestWithRetry como un
// *httpclient.RequestError. SIEMPRE Recoverable, vía
// accountmanager.Manager.ReportFailureAs con la clasificación forzada por el
// llamador (punto 3 de la cabecera de handler.go; misma lógica que
// routesopenai.handleTransportError).
func (h *Handler) handleTransportError(acc *accountmanager.Account, model string, reqErr error) (status int, message string, done bool) {
	info := networkerrors.Info{UserMessage: reqErr.Error(), SuggestedHTTPCode: http.StatusBadGateway}

	var rerr *httpclient.RequestError
	if errors.As(reqErr, &rerr) {
		info = rerr.Info
	}

	h.accounts.ReportFailureAs(acc.ID, model, accounterrors.Recoverable, info.SuggestedHTTPCode, "", info.UserMessage)
	return info.SuggestedHTTPCode, info.UserMessage, false
}

// handleKiroError trata una respuesta HTTP no-2xx de Kiro. Port de
// routes_anthropic.py:518-668: lee el cuerpo (fallback "Unknown error" si la
// lectura falla o el cuerpo llega vacío, routes_anthropic.py:520-523), lo
// enriquece vía kiroerrors, y clasifica con accounterrors (a través de
// accountmanager.Manager.ReportFailure). Fatal → el error real de Kiro al
// cliente en dialecto Anthropic (done=true); Recoverable → done=false para
// seguir con la siguiente cuenta.
func (h *Handler) handleKiroError(w http.ResponseWriter, acc *accountmanager.Account, model string, resp *http.Response) (status int, message string, done bool) {
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil || len(body) == 0 {
		body = []byte("Unknown error")
	}

	reason, userMessage := parseKiroError(body)
	classification := h.accounts.ReportFailure(acc.ID, model, resp.StatusCode, reason, userMessage)

	if classification == accounterrors.Fatal {
		writeAnthropicError(w, resp.StatusCode, "api_error", userMessage)
		return 0, "", true
	}
	return resp.StatusCode, userMessage, false
}

// parseKiroError decodifica el cuerpo de error de Kiro y lo enriquece vía
// kiroerrors.Enhance. Si no es JSON válido, reason queda vacío y userMessage es
// el texto crudo del cuerpo (routes_anthropic.py:528-548).
func parseKiroError(body []byte) (reason, userMessage string) {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", string(body)
	}
	info := kiroerrors.Enhance(parsed)
	return info.Reason, info.UserMessage
}

// writeExhausted maneja el agotamiento de cuentas (GetNextAccount →
// *ExhaustedAccountsError). Port de routes_anthropic.py:337-365, con el
// dialecto de error Anthropic: cuenta única → status/mensaje del último fallo
// real (o 503); múltiples cuentas → 503 genérico con contexto del último
// fallo.
func (h *Handler) writeExhausted(w http.ResponseWriter, err error, attempted bool, lastStatus int, lastMessage string) {
	var exhausted *accountmanager.ExhaustedAccountsError
	if !errors.As(err, &exhausted) {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "internal error selecting account: "+err.Error())
		return
	}

	total := len(h.accounts.Accounts())
	if total <= 1 {
		status := lastStatus
		if status == 0 {
			status = http.StatusServiceUnavailable
		}
		message := lastMessage
		if message == "" {
			message = exhausted.LastMsg
		}
		if message == "" {
			message = "Account unavailable"
		}
		writeAnthropicError(w, status, "api_error", message)
		return
	}

	detail := "No available accounts for this model."
	if attempted {
		detail = "All accounts failed after full circle."
	}
	context := lastMessage
	if context == "" {
		context = exhausted.LastMsg
	}
	if context != "" {
		detail += " Error from last account: " + context
	}
	writeAnthropicError(w, http.StatusServiceUnavailable, "api_error", detail)
}
