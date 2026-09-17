// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package debugmiddleware

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/debuglogger"
)

// captureNext es el handler final del chain: expone lo que ve (context +
// cuerpo tal como llega tras el middleware) para que los tests aserten sobre
// ello, y siempre responde con el status dado.
func captureNext(status int, gotLogger **debuglogger.DebugLogger, gotBody *[]byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*gotLogger = FromContext(r.Context())
		if gotBody != nil {
			b, _ := io.ReadAll(r.Body)
			*gotBody = b
		}
		w.WriteHeader(status)
	}
}

// --- Escenario 1 (brief): POST /v1/chat/completions con DEBUG_MODE=all →
// hay un *DebugLogger en el context del handler.

func TestChatCompletions_DebugModeAll_LoggerInContext(t *testing.T) {
	cfg := &config.Config{DebugMode: "all", DebugDir: t.TempDir()}
	mw := New(cfg)

	var gotLogger *debuglogger.DebugLogger
	next := captureNext(http.StatusOK, &gotLogger, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewReader([]byte(`{"model":"gpt-4","messages":[]}`)))
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if gotLogger == nil {
		t.Fatal("FromContext(ctx) = nil, quiero un *DebugLogger")
	}
}

// Mismo escenario para /v1/messages (la otra ruta LOGGED_ENDPOINTS de
// debug_middleware.py:47-50).
func TestMessages_DebugModeAll_LoggerInContext(t *testing.T) {
	cfg := &config.Config{DebugMode: "all", DebugDir: t.TempDir()}
	mw := New(cfg)

	var gotLogger *debuglogger.DebugLogger
	next := captureNext(http.StatusOK, &gotLogger, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if gotLogger == nil {
		t.Fatal("FromContext(ctx) = nil, quiero un *DebugLogger")
	}
}

// --- Escenario 2 (brief): un cuerpo inválido (forma-422) con modo "all"
// igualmente prepara el logger (antes de validación) — el middleware corre
// antes que cualquier parseo/validación del handler downstream, así que el
// cuerpo crudo (inválido) debe quedar volcado en DEBUG_DIR/request_body.json
// pese a que el "handler" simulado abajo devuelva 422.

func TestChatCompletions_InvalidBody_StillPreparesLoggerBeforeValidation(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DebugMode: "all", DebugDir: dir}
	mw := New(cfg)

	invalidBody := []byte(`{not valid json`)

	var gotLogger *debuglogger.DebugLogger
	var gotBody []byte
	next := captureNext(http.StatusUnprocessableEntity, &gotLogger, &gotBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(invalidBody))
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if gotLogger == nil {
		t.Fatal("FromContext(ctx) = nil, quiero un *DebugLogger incluso con cuerpo inválido")
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, quiero 422 (el middleware NO debe cortocircuitar la petición)", rec.Code)
	}
	if !bytes.Equal(gotBody, invalidBody) {
		t.Fatalf("cuerpo visto por el handler = %q, quiero %q (el re-wrap debe preservar los bytes originales)",
			gotBody, invalidBody)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "request_body.json"))
	if err != nil {
		t.Fatalf("request_body.json no se escribió (PrepareNewRequest+LogRequestBody deben correr antes de validar): %v", err)
	}
	if !bytes.Equal(raw, invalidBody) {
		t.Errorf("request_body.json = %q, quiero %q (no es JSON válido → bytes crudos)", raw, invalidBody)
	}
}

// --- Escenario 3 (brief): /health no prepara logger (fuera de scope).

func TestHealth_OutOfScope_NoLoggerInContext(t *testing.T) {
	cfg := &config.Config{DebugMode: "all", DebugDir: t.TempDir()}
	mw := New(cfg)

	var gotLogger *debuglogger.DebugLogger
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		gotLogger = FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if !called {
		t.Fatal("next no fue invocado para /health")
	}
	if gotLogger != nil {
		t.Errorf("FromContext(ctx) = %v, quiero nil para /health (fuera de LOGGED_ENDPOINTS)", gotLogger)
	}
}

// Otras rutas fuera de scope: GET /v1/models, GET /{$}, POST
// /v1/messages/count_tokens — el middleware NUNCA debe tocarlas
// (debug_middleware.py:82-83, LOGGED_ENDPOINTS solo tiene 2 entradas).
func TestOtherRoutes_OutOfScope_NoLoggerInContext(t *testing.T) {
	cfg := &config.Config{DebugMode: "all", DebugDir: t.TempDir()}
	mw := New(cfg)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/"},
		{http.MethodGet, "/v1/models"},
		{http.MethodPost, "/v1/messages/count_tokens"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var gotLogger *debuglogger.DebugLogger
			next := captureNext(http.StatusOK, &gotLogger, nil)

			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			mw(next).ServeHTTP(rec, req)

			if gotLogger != nil {
				t.Errorf("FromContext(ctx) = %v, quiero nil para %s %s", gotLogger, tc.method, tc.path)
			}
		})
	}
}

// --- DEBUG_MODE=off: el logger igual viaja en el context (closure-captured,
// single-flight — spec del brief), pero PrepareNewRequest/LogRequestBody NO
// corren: nada se escribe a DEBUG_DIR y el cuerpo llega intacto sin haber
// sido tocado por el middleware.

func TestChatCompletions_DebugModeOff_LoggerPresentButInert(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DebugMode: "off", DebugDir: dir}
	mw := New(cfg)

	body := []byte(`{"model":"gpt-4","messages":[]}`)
	var gotLogger *debuglogger.DebugLogger
	var gotBody []byte
	next := captureNext(http.StatusOK, &gotLogger, &gotBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if gotLogger == nil {
		t.Fatal("FromContext(ctx) = nil, quiero el *DebugLogger inyectado (inerte, pero presente)")
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("cuerpo visto por el handler = %q, quiero %q", gotBody, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "request_body.json")); !os.IsNotExist(err) {
		t.Errorf("request_body.json no debería existir en modo off (stat err=%v)", err)
	}
}

// --- FromContext sin nada instalado (p.ej. petición nunca pasó por el
// middleware) → nil, sin panic.

func TestFromContext_NoValue_ReturnsNil(t *testing.T) {
	if got := FromContext(context.Background()); got != nil {
		t.Errorf("FromContext(context.Background()) = %v, quiero nil", got)
	}
}

// --- Métodos no-POST a las 2 rutas de LOGGED_ENDPOINTS quedan fuera de
// scope: debug_middleware.py no distingue por método (Starlette solo
// registra POST en esas rutas — routes_openai.py/routes_anthropic.py), pero
// el mux real de internal/server tampoco monta GET en esos paths, así que
// esto documenta la decisión: New() exige POST explícitamente.
func TestChatCompletionsPath_WrongMethod_OutOfScope(t *testing.T) {
	cfg := &config.Config{DebugMode: "all", DebugDir: t.TempDir()}
	mw := New(cfg)

	var gotLogger *debuglogger.DebugLogger
	next := captureNext(http.StatusOK, &gotLogger, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if gotLogger != nil {
		t.Errorf("FromContext(ctx) = %v, quiero nil para GET /v1/chat/completions", gotLogger)
	}
}
