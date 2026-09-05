// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package utils

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	lowerHexRe = regexp.MustCompile(`^[0-9a-f]+$`)
	uuidRe     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// TestMachineFingerprintIsStable comprueba que dos llamadas devuelven el mismo
// valor, que tiene 64 caracteres y que solo contiene dígitos hexadecimales en
// minúscula. Las tres son propiedades de la fórmula sha256 hexdigest y son
// independientes de la máquina.
func TestMachineFingerprintIsStable(t *testing.T) {
	a := MachineFingerprint()
	b := MachineFingerprint()
	if a != b {
		t.Fatalf("dos llamadas devolvieron valores distintos: %q vs %q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("longitud esperada 64, obtenida %d: %q", len(a), a)
	}
	if !lowerHexRe.MatchString(a) {
		t.Fatalf("no es hex minúsculo: %q", a)
	}
}

// TestMachineFingerprintMatchesTheFormula comprueba la FÓRMULA
// sha256("{hostname}-{username}-kiro-gateway") derivando el valor esperado a
// mano con los mismos datos que usa la producción. No compara contra un valor
// hard-coded ni contra el corpus: fase 1 retiró ese objetivo a propósito
// (docs/CORPUS.md §4) porque el valor depende de la máquina que grabó.
func TestMachineFingerprintMatchesTheFormula(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Skipf("os.Hostname falló: %v", err)
	}
	// Réplica exacta de la lógica de kiroUsername() en fingerprint.go. Si
	// diverge, el test detecta la fuga.
	var username string
	for _, name := range []string{"LOGNAME", "USER", "LNAME", "USERNAME"} {
		if v, ok := os.LookupEnv(name); ok && v != "" {
			username = v
			break
		}
	}
	if username == "" {
		u, err := user.Current()
		if err != nil {
			t.Skipf("sin nombre de usuario disponible: %v", err)
		}
		username = u.Username
	}
	if hostname == "" || username == "" {
		t.Skipf("hostname o username vacíos, no se puede verificar la fórmula")
	}
	sum := sha256.Sum256([]byte(hostname + "-" + username + "-kiro-gateway"))
	expected := hex.EncodeToString(sum[:])
	if got := MachineFingerprint(); got != expected {
		t.Fatalf("MachineFingerprint = %q, esperado %q (por fórmula)", got, expected)
	}
}

// TestIDFormats fija el prefijo y la longitud exacta de cada generador de
// identificadores. Los formatos salen de utils.py y streaming_anthropic.py:
//
//	chatcmpl-<32 hex>   ← generate_completion_id       (utils.py)
//	call_<8 hex>        ← generate_tool_call_id        (utils.py)
//	msg_<24 hex>        ← generate_message_id          (streaming_anthropic.py)
//	sig_<32 hex>        ← generate_thinking_signature  (streaming_anthropic.py)
func TestIDFormats(t *testing.T) {
	cases := []struct {
		name   string
		got    string
		prefix string
		length int
	}{
		{"completion", GenerateCompletionID(), "chatcmpl-", 9 + 32},
		{"toolcall", GenerateToolCallID(), "call_", 5 + 8},
		{"message", GenerateMessageID(), "msg_", 4 + 24},
		{"thinking", GenerateThinkingSignature(), "sig_", 4 + 32},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.HasPrefix(tc.got, tc.prefix) {
				t.Fatalf("prefijo esperado %q, valor %q", tc.prefix, tc.got)
			}
			if len(tc.got) != tc.length {
				t.Fatalf("longitud esperada %d, obtenida %d en %q", tc.length, len(tc.got), tc.got)
			}
			suffix := tc.got[len(tc.prefix):]
			if !lowerHexRe.MatchString(suffix) {
				t.Fatalf("sufijo no es hex minúsculo: %q", suffix)
			}
		})
	}
}

// TestIDGeneratorsAreInjectable verifica el punto de inyección (contrato
// docs/CORPUS.md §4): las fases 4 y 5 sustituyen NewUUID/NewHexToken por
// versiones deterministas para comparar contra ids congelados en el corpus.
// Este test demuestra que el seam existe y funciona.
func TestIDGeneratorsAreInjectable(t *testing.T) {
	origHex, origUUID := NewHexToken, NewUUID
	t.Cleanup(func() { NewHexToken, NewUUID = origHex, origUUID })

	NewHexToken = func() string { return "0000000000000000000000000000002a" }
	NewUUID = func() string { return "00000001-0000-4000-8000-000000000042" }

	if got := GenerateCompletionID(); got != "chatcmpl-0000000000000000000000000000002a" {
		t.Fatalf("completion id = %q", got)
	}
	if got := GenerateToolCallID(); got != "call_00000000" {
		t.Fatalf("tool call id = %q", got)
	}
	if got := GenerateMessageID(); got != "msg_000000000000000000000000" {
		t.Fatalf("message id = %q", got)
	}
	if got := GenerateThinkingSignature(); got != "sig_0000000000000000000000000000002a" {
		t.Fatalf("thinking sig = %q", got)
	}
	// La rama aleatoria de conversation_id consume NewUUID.
	if got := GenerateConversationID(nil); got != "00000001-0000-4000-8000-000000000042" {
		t.Fatalf("conversation id fallback = %q", got)
	}
}

// TestConversationIDFromMessagesIsStable cubre las tres propiedades de la
// función: entradas iguales dan salidas iguales, entradas distintas dan
// salidas distintas, y sin mensajes cada llamada devuelve un uuid nuevo.
func TestConversationIDFromMessagesIsStable(t *testing.T) {
	a := []map[string]any{{"role": "user", "content": "Hola"}}
	b := []map[string]any{{"role": "user", "content": "Hola"}}
	c := []map[string]any{{"role": "user", "content": "Adios"}}

	if x, y := GenerateConversationID(a), GenerateConversationID(b); x != y {
		t.Fatalf("mensajes iguales dieron ids distintos: %q vs %q", x, y)
	}
	if GenerateConversationID(a) == GenerateConversationID(c) {
		t.Fatalf("mensajes distintos dieron el mismo id")
	}
	if got := GenerateConversationID(a); len(got) != 16 {
		t.Fatalf("rama del hash: longitud esperada 16, obtenida %d en %q", len(got), got)
	}
}

// TestConversationIDWithoutMessagesIsRandom fija la rama de respaldo: nil y
// slice vacío deben coger la rama uuid4 y devolver valores distintos entre
// llamadas.
func TestConversationIDWithoutMessagesIsRandom(t *testing.T) {
	id1 := GenerateConversationID(nil)
	id2 := GenerateConversationID(nil)
	if id1 == id2 {
		t.Fatalf("nil dos veces dio el mismo id: %q", id1)
	}
	if len(id1) != 36 {
		t.Fatalf("uuid fallback: longitud esperada 36, obtenida %d en %q", len(id1), id1)
	}
	id3 := GenerateConversationID([]map[string]any{})
	if id3 == id1 || id3 == id2 {
		t.Fatalf("slice vacío colisionó con nil: %q", id3)
	}
}

// TestConversationIDHashBranchMatchesAlgorithm verifica la rama del hash a
// mano, replicando el algoritmo del original aquí. Es la comprobación que
// pide el brief: el único caso golden que aparece en el corpus para este
// generador (00000001-0000-4000-8000-000000000001) es el uuid determinista
// inyectado durante la grabación y corresponde a la rama aleatoria, no a
// esta. Ver docs/CORPUS.md §5.
func TestConversationIDHashBranchMatchesAlgorithm(t *testing.T) {
	messages := []map[string]any{
		{"role": "user", "content": "Hola"},
		{"role": "assistant", "content": "Que tal"},
	}
	// Réplica del algoritmo: simplificación con las mismas claves y
	// json.Marshal (Go ordena las claves de map[string]X de forma nativa).
	simplified := []map[string]string{
		{"role": "user", "content": "Hola"},
		{"role": "assistant", "content": "Que tal"},
	}
	raw, err := json.Marshal(simplified)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sum := sha256.Sum256(raw)
	expected := hex.EncodeToString(sum[:])[:16]

	if got := GenerateConversationID(messages); got != expected {
		t.Fatalf("GenerateConversationID = %q, esperado %q", got, expected)
	}
}

// TestConversationIDTakesFirstThreeAndLast fija la ventana de mensajes que el
// original toma cuando la conversación crece: los tres primeros y el último.
// Cambiar los mensajes intermedios (índices 3..len-2) NO debe cambiar el id.
func TestConversationIDTakesFirstThreeAndLast(t *testing.T) {
	build := func(mid string) []map[string]any {
		return []map[string]any{
			{"role": "user", "content": "m0"},
			{"role": "assistant", "content": "m1"},
			{"role": "user", "content": "m2"},
			{"role": "assistant", "content": mid},
			{"role": "user", "content": "final"},
		}
	}
	a := GenerateConversationID(build("intermedio-A"))
	b := GenerateConversationID(build("intermedio-B"))
	if a != b {
		t.Fatalf("cambiar un mensaje intermedio cambió el id: %q vs %q", a, b)
	}
}

// -----------------------------------------------------------------------------
// Tarea 4: cabeceras salientes.
// -----------------------------------------------------------------------------

// fakeTokenProvider satisface utils.TokenProvider sin tocar red. Los tests lo
// usan para inyectar un token conocido y verificar que llega a la cabecera
// Authorization tal cual.
type fakeTokenProvider struct {
	token      string
	profileARN string
	err        error
}

func (f *fakeTokenProvider) AccessToken(context.Context) (string, error) {
	return f.token, f.err
}

func (f *fakeTokenProvider) ProfileARN() string { return f.profileARN }

// TestKiroHeadersMatchUpstream cubre las nueve cabeceras que Kiro espera,
// verificadas contra .upstream/kiro/utils.py :: get_kiro_headers.
//
// Notas de paridad byte a byte:
//   - Las dos cabeceras que embeben la huella (User-Agent y x-amz-user-agent)
//     se construyen aquí con MachineFingerprint() para no fijar un valor que
//     depende de la máquina que corre el test.
//   - amz-sdk-invocation-id es un uuid nuevo por llamada: se comprueba forma
//     y se hace una segunda llamada para verificar que cambia.
//   - Las claves se acceden directamente por el mapa (h["x-amz-target"]) en
//     vez de por http.Header.Get, que canonicaliza a "X-Amz-Target" y no
//     encontraría la clave. Ver el informe de la fase para la nota sobre
//     capitalización.
func TestKiroHeadersMatchUpstream(t *testing.T) {
	tp := &fakeTokenProvider{token: "tk-abc", profileARN: "arn:aws:codewhisperer:..."}
	h, err := GetKiroHeaders(context.Background(), tp)
	if err != nil {
		t.Fatalf("GetKiroHeaders: %v", err)
	}

	if got := len(h); got != 9 {
		var keys []string
		for k := range h {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("se esperaban 9 cabeceras, hay %d: %v", got, keys)
	}

	fp := MachineFingerprint()
	wantUserAgent := "aws-sdk-js/1.0.27 ua/2.1 os/win32#10.0.19044 lang/js md/nodejs#22.21.1 api/codewhispererstreaming#1.0.27 m/E KiroIDE-0.7.45-" + fp
	wantXAmzUserAgent := "aws-sdk-js/1.0.27 KiroIDE-0.7.45-" + fp

	fixed := map[string]string{
		"Authorization":               "Bearer tk-abc",
		"Content-Type":                "application/x-amz-json-1.0",
		"x-amz-target":                "AmazonCodeWhispererStreamingService.GenerateAssistantResponse",
		"User-Agent":                  wantUserAgent,
		"x-amz-user-agent":            wantXAmzUserAgent,
		"x-amzn-codewhisperer-optout": "true",
		"x-amzn-kiro-agent-mode":      "vibe",
		"amz-sdk-request":             "attempt=1; max=3",
	}
	for key, want := range fixed {
		values, ok := h[key]
		if !ok {
			t.Errorf("falta cabecera %q", key)
			continue
		}
		if len(values) != 1 {
			t.Errorf("cabecera %q con %d valores, esperado 1", key, len(values))
			continue
		}
		if values[0] != want {
			t.Errorf("cabecera %q = %q, esperado %q", key, values[0], want)
		}
	}

	// User-Agent va en una sola línea: sin saltos.
	if strings.ContainsAny(h["User-Agent"][0], "\r\n") {
		t.Errorf("User-Agent contiene saltos de línea: %q", h["User-Agent"][0])
	}

	// amz-sdk-invocation-id: uuid válido y distinto en dos llamadas.
	invocID := h["amz-sdk-invocation-id"]
	if len(invocID) != 1 {
		t.Fatalf("amz-sdk-invocation-id ausente o mal formada: %v", invocID)
	}
	if !uuidRe.MatchString(invocID[0]) {
		t.Fatalf("amz-sdk-invocation-id no es un uuid válido: %q", invocID[0])
	}

	h2, err := GetKiroHeaders(context.Background(), tp)
	if err != nil {
		t.Fatalf("segunda llamada a GetKiroHeaders: %v", err)
	}
	if h2["amz-sdk-invocation-id"][0] == invocID[0] {
		t.Fatalf("dos llamadas dieron el mismo amz-sdk-invocation-id: %q", invocID[0])
	}
}

// TestKiroHeadersPropagateTokenError verifica que un fallo del TokenProvider
// aborta la construcción de cabeceras en vez de emitir un Bearer vacío.
func TestKiroHeadersPropagateTokenError(t *testing.T) {
	sentinel := errors.New("sin token")
	tp := &fakeTokenProvider{err: sentinel}
	h, err := GetKiroHeaders(context.Background(), tp)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, esperado envolver a %v", err, sentinel)
	}
	if h != nil {
		t.Fatalf("con error se esperaba http.Header nil, hay %v", h)
	}
}

// TestKiroHeadersPreserveCase documenta que el mapa se escribe con las claves
// tal cual las manda el original (x-amz-target en minúscula, no
// X-Amz-Target). Si un consumidor futuro sustituye la construcción del mapa
// por http.Header.Set, este test lo detecta.
func TestKiroHeadersPreserveCase(t *testing.T) {
	tp := &fakeTokenProvider{token: "tk"}
	h, err := GetKiroHeaders(context.Background(), tp)
	if err != nil {
		t.Fatalf("GetKiroHeaders: %v", err)
	}
	for _, key := range []string{
		"x-amz-target",
		"x-amz-user-agent",
		"x-amzn-codewhisperer-optout",
		"x-amzn-kiro-agent-mode",
		"amz-sdk-invocation-id",
		"amz-sdk-request",
	} {
		if _, ok := h[key]; !ok {
			t.Errorf("clave %q no está literal en el mapa (¿canonicalización?)", key)
		}
	}
}
