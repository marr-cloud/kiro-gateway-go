// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesopenai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/marr-cloud/kiro-gateway-go/internal/accounterrors"
	"github.com/marr-cloud/kiro-gateway-go/internal/accountmanager"
	"github.com/marr-cloud/kiro-gateway-go/internal/convertersopenai"
	"github.com/marr-cloud/kiro-gateway-go/internal/httpclient"
	"github.com/marr-cloud/kiro-gateway-go/internal/kiroerrors"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
	"github.com/marr-cloud/kiro-gateway-go/internal/networkerrors"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// ChatCompletions responde POST /v1/chat/completions. Port de
// routes_openai.py:160-556 (rama de sistema de cuentas; la rama "legacy" sin
// failover de routes_openai.py:558-769 no se porta — este servicio siempre
// corre con accountmanager.Manager, ver docs/MAPPING.md).
func (h *Handler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "error reading request body: "+err.Error())
		return
	}

	var req modelsopenai.ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid JSON in request body: "+err.Error())
		return
	}

	h.failoverChatCompletions(r.Context(), w, &req)
}

// failoverChatCompletions es el bucle de failover multi-cuenta, port de
// routes_openai.py:292-556 simplificado según el brief de esta tarea: en vez
// de un contador MAX_ATTEMPTS explícito, el bucle confía en que
// accountmanager.Manager.GetNextAccount ya excluye tanto el conjunto
// `exclude` (cuentas probadas en ESTA petición) como las cuentas en
// cuarentena por fallos de peticiones anteriores — así que agota
// naturalmente en *accountmanager.ExhaustedAccountsError sin necesidad de
// contar intentos por fuera.
func (h *Handler) failoverChatCompletions(ctx context.Context, w http.ResponseWriter, req *modelsopenai.ChatCompletionRequest) {
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

// attemptAccount intenta UNA cuenta. Si termina la petición (éxito, o un
// error Fatal de Kiro que se propaga al cliente), escribe la respuesta en w
// y devuelve done=true. Si el fallo es Recoverable, devuelve done=false con
// el status/mensaje para recordar en caso de agotamiento final; el llamador
// debe excluir acc.ID y probar la siguiente cuenta.
func (h *Handler) attemptAccount(ctx context.Context, w http.ResponseWriter, acc *accountmanager.Account, req *modelsopenai.ChatCompletionRequest) (status int, message string, done bool) {
	conversationID := utils.GenerateConversationID(nil)

	// profileArn es obligatorio para runtime.kiro.dev en todos los tipos de
	// auth. routes_openai.py:327: `auth_manager.profile_arn or PROFILE_ARN
	// or ""`.
	profileArn := acc.Auth.ProfileARN()
	if profileArn == "" {
		profileArn = h.cfg.ProfileARN
	}

	payloadResult := convertersopenai.BuildKiroPayload(req, conversationID, profileArn)
	payloadBytes, err := json.Marshal(payloadResult.Payload)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "failed to encode Kiro payload: "+err.Error())
		return 0, "", true
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, h.apiURL(acc), bytes.NewReader(payloadBytes))
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "failed to build Kiro request: "+err.Error())
		return 0, "", true
	}

	// Kiro SIEMPRE responde en modo streaming — ver el punto 3 del
	// comentario de cabecera de handler.go. req.Stream no entra aquí.
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
// rechazada, reintentos agotados...) devuelto por RequestWithRetry como un
// *httpclient.RequestError. Ver el punto 4 del comentario de cabecera de
// handler.go: SIEMPRE se trata como Recoverable, igual que
// routes_openai.py:512-517 (`ErrorType.RECOVERABLE` pasado explícitamente al
// llamador). accountmanager.Manager.ReportFailureAs toma esa clasificación
// del LLAMADOR en vez de derivarla de accounterrors.Classify(statusCode,
// reason) — que trataría cualquier SuggestedHTTPCode 5xx como Fatal,
// correcto para una respuesta HTTP real de Kiro pero no para un fallo de
// transporte — así que arma el circuit breaker del mismo modo que una
// respuesta 402/403/429 real (account_manager.py:809-816).
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
// routes_openai.py:443-504: lee el cuerpo, lo enriquece vía kiroerrors, y
// clasifica con accounterrors (a través de
// accountmanager.Manager.ReportFailure, que aplica accounterrors.Classify
// internamente). Fatal → el error real de Kiro se devuelve al cliente
// inmediatamente (done=true). Recoverable → se devuelve para que el
// llamador excluya la cuenta y siga con la siguiente (done=false).
//
// Si leer el cuerpo falla, o el cuerpo llega vacío, se usa el literal
// "Unknown error" — igual que el fallback de routes_openai.py:446-448
// (`except Exception: error_content = b"Unknown error"`) para el caso de
// error de lectura, ampliado aquí también al caso de cuerpo vacío para que
// el userMessage propagado al cliente nunca sea "".
func (h *Handler) handleKiroError(w http.ResponseWriter, acc *accountmanager.Account, model string, resp *http.Response) (status int, message string, done bool) {
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil || len(body) == 0 {
		body = []byte("Unknown error")
	}

	reason, userMessage := parseKiroError(body)
	classification := h.accounts.ReportFailure(acc.ID, model, resp.StatusCode, reason, userMessage)

	if classification == accounterrors.Fatal {
		writeOpenAIError(w, resp.StatusCode, userMessage)
		return 0, "", true
	}
	return resp.StatusCode, userMessage, false
}

// parseKiroError decodifica el cuerpo de error de Kiro y lo enriquece vía
// kiroerrors.Enhance. Si el cuerpo no es JSON válido, reason queda vacío
// (equivalente al `error_reason = None` del original cuando json.loads
// falla, routes_openai.py:463-465) y userMessage es el texto crudo del
// cuerpo.
func parseKiroError(body []byte) (reason, userMessage string) {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", string(body)
	}
	info := kiroerrors.Enhance(parsed)
	return info.Reason, info.UserMessage
}

// writeExhausted maneja el error que devuelve GetNextAccount cuando no
// queda ninguna cuenta disponible. Port de la lógica de
// routes_openai.py:299-312/543-556, con el dialecto de error unificado (ver
// el punto 2 del comentario de cabecera de handler.go): sin cuenta única, un
// 503 genérico; con una sola cuenta configurada, se propaga el
// status/mensaje del último fallo real si lo hay.
func (h *Handler) writeExhausted(w http.ResponseWriter, err error, attempted bool, lastStatus int, lastMessage string) {
	var exhausted *accountmanager.ExhaustedAccountsError
	if !errors.As(err, &exhausted) {
		writeOpenAIError(w, http.StatusInternalServerError, "internal error selecting account: "+err.Error())
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
		writeOpenAIError(w, status, message)
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
	writeOpenAIError(w, http.StatusServiceUnavailable, detail)
}
