// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// TestBuildKiroHistory valida BuildKiroHistory contra los 37 casos grabados
// de kiro.converters_core:build_kiro_history
// (.upstream/kiro/converters_core.py:1320-1398).
//
// La entrada es posicional (args=[messages, model_id]), no kwargs — se
// reutilizan decodeMessages (normalize_test.go) para el primer argumento.
// build_kiro_history es una función pura sin dependencia de configuración
// (no llama a nada de thinking.go), así que el test no toca ningún package
// var. Verificado además ejecutando el upstream real (commit a5292ca) contra
// los 37 casos: los 37 coinciden byte a byte, sin ninguno defectuoso — a
// diferencia de build_kiro_payload (ver TestBuildKiroPayload), aquí no hace
// falta excluir ningún caso.
func TestBuildKiroHistory(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_core/build_kiro_history")
	if len(cases) != 37 {
		t.Fatalf("se esperaban 37 casos de build_kiro_history, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			msgs := decodeMessages(t, testutil.Arg(t, c.Input, 0), c.Name)

			var modelID string
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 1), &modelID); err != nil {
				t.Fatalf("case %s: decodificando model_id: %v", c.Name, err)
			}

			got := BuildKiroHistory(msgs, modelID)
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}

// knownDefectiveBuildKiroPayloadCases son los 15 casos de
// testdata/converters_core/build_kiro_payload cuyo "output" grabado NO
// refleja lo que el upstream real produce para su "input" grabado —
// verificado ejecutando kiro.converters_core.build_kiro_payload tal cual, en
// el commit fijado a5292ca (el mismo que testdata graba en
// "recorded_at_commit" y el mismo que tiene en checkout .upstream, ver
// Taskfile UPSTREAM_COMMIT), directamente contra el "input" de cada uno de
// estos 15 casos.
//
// Causa raíz (mecanismo, no solo síntoma): build_kiro_payload
// (.upstream/kiro/converters_core.py:1488-1494) MUTA in-place el primer
// mensaje de history_messages para anteponerle full_system_prompt:
//
//	first_msg = history_messages[0]
//	if first_msg.role == "user":
//	    first_msg.content = f"{full_system_prompt}\n\n{original_content}"
//
// Cuando ese primer mensaje de history_messages sobrevive con la MISMA
// identidad de objeto que el messages[0] original pasado por el llamador
// (el caso normal: primer mensaje ya role="user", sin fusión ni
// normalización que lo reconstruya), la mutación golpea el objeto que
// tools/corpus/recorder.py:_wrap_function todavía referencia en `args`:
//
//	result = original(*args, **kwargs)
//	_record_function(target, args, kwargs, result, config)   # DESPUÉS
//
// Esto es la misma familia de defecto que ya documentó la Task 6 para
// merge_adjacent_messages / ensure_assistant_before_tool_results
// (knownCorpusInputAliasingDefects, normalize_test.go): el grabador lee
// `args`/`kwargs` DESPUÉS de invocar la función original, así que un mensaje
// mutado en el sitio contamina lo que se graba. La diferencia aquí es que
// además de contaminar "input" (que no se usa para comparar), aparentemente
// también deja "output" grabado sin el prefijo — reproducible
// determinísticamente ejecutando el upstream real contra estos 15 "input"
// grabados: la salida real siempre tiene a "want" como sufijo estricto de lo
// que produce "got" en history[0] (falta exactamente el prefijo de
// full_system_prompt), nunca al revés, y nunca un tercer patrón de
// divergencia — descarta un bug de traducción en este port, que reproduce
// el algoritmo tal cual está escrito en el upstream (líneas 1435-1597,
// traducidas literalmente en payload.go).
//
// Verificación reproducible (usada para generar esta lista): ejecutar
// .upstream/.venv/{Scripts/python.exe,bin/python} contra
// .upstream/kiro/converters_core.py, aplicando cada caso.input.config como
// setattr sobre kiro.converters_core (y kiro.config, por si alguna función
// hace `from kiro.config import X` en el cuerpo, como
// get_truncation_recovery_system_addition), reconstruyendo
// UnifiedMessage/UnifiedTool/ThinkingConfig desde caso.input.kwargs, y
// comparando build_kiro_payload(...).payload contra caso.output.payload.
var knownDefectiveBuildKiroPayloadCases = map[string]string{
	"203e85f5cfe4a55b": "history[0] falta el prefijo de full_system_prompt; además history[1] difiere en el merge de dos assistant adyacentes (mismo mecanismo de aliasing, efecto compuesto)",
	"20b891fba288f627": "history[0] falta el prefijo de full_system_prompt",
	"338483fd6ce83fff": "history[0] falta el prefijo de full_system_prompt",
	"407b9df65f0bce57": "history[0] falta el prefijo de full_system_prompt",
	"58b265aeb61dc5c0": "history[0] falta el prefijo de full_system_prompt",
	"6c5ce18304794000": "history[0] falta el prefijo de full_system_prompt",
	"71c14ed39119341b": "history[0] falta el prefijo de full_system_prompt",
	"a1d3dc32229b6ce9": "history[0] falta el prefijo de full_system_prompt",
	"a4aa47e446913506": "history[0] falta el prefijo de full_system_prompt",
	"a6e4061e66a122f0": "history[0] falta el prefijo de full_system_prompt",
	"aa988020472dd4da": "history[0] falta el prefijo de full_system_prompt",
	"dedadfd94f77613c": "history[0] falta el prefijo de full_system_prompt",
	"e715c3825e439409": "history[0] falta el prefijo de full_system_prompt; además currentMessage difiere (mismo mecanismo de aliasing sobre el merge de mensajes user adyacentes, efecto compuesto)",
	"f765166cda011175": "history[0] falta el prefijo de full_system_prompt",
	"ff714595896aac7e": "history[0] falta el prefijo de full_system_prompt",
}

// buildKiroPayloadOutput es la forma de "output" que graba el corpus de
// build_kiro_payload: el dataclass KiroPayloadResult serializado con sus
// propios nombres de campo Python (payload, tool_documentation).
type buildKiroPayloadOutput struct {
	Payload           json.RawMessage `json:"payload"`
	ToolDocumentation string          `json:"tool_documentation"`
}

// TestBuildKiroPayload valida BuildKiroPayload contra los 83 casos grabados
// de kiro.converters_core:build_kiro_payload
// (.upstream/kiro/converters_core.py:1405-1597), con la excepción documentada
// de knownDefectiveBuildKiroPayloadCases (ver su comentario).
//
// Firma real de BuildKiroPayload: SIETE argumentos, en el mismo orden que el
// original — messages, systemPrompt, modelID, tools, conversationID,
// profileArn, thinkingCfg — no ocho, y NO lleva truncationRecovery ni
// toolDescriptionMaxLength como parámetros. El brief de la tarea describía
// una firma de ocho argumentos (con conversationID "añadido" al final,
// truncationRecovery y toolDescriptionMaxLength como parámetros propios, y
// sin profileArn) que no sobrevive la verificación contra el upstream real:
//
//   - El original YA recibe conversation_id como su quinto parámetro
//     posicional — no hay nada que "añadir"; los 83 casos del corpus lo
//     confirman como kwarg siempre presente (nunca ausente ni "").
//   - El original TAMBIÉN recibe profile_arn (sexto parámetro) — ausente
//     por completo de la firma que proponía el brief. Los 83 casos lo
//     llevan como kwarg (14 con profile_arn=""), y el payload grabado omite
//     la clave "profileArn" exactamente en esos 14 casos (`if profile_arn:`
//     en el original) — sin este parámetro no hay forma de reproducir esos
//     14 casos.
//   - truncation_recovery NO es un parámetro del original: la única
//     bandera que build_kiro_payload consulta es TRUNCATION_RECOVERY como
//     global de kiro.config (vía get_truncation_recovery_system_addition(),
//     que ya no toma argumentos — Task 7). El corpus lo confirma:
//     TRUNCATION_RECOVERY vive en input.config en los 83 casos, nunca en
//     input.kwargs.
//   - tool_description_max_length TAMPOCO es un parámetro del original: es
//     TOOL_DESCRIPTION_MAX_LENGTH, otro global de kiro.config, presente en
//     input.config de los 83 casos y nunca en input.kwargs. Este port sí
//     necesita proveérselo a ProcessToolsWithLongDescriptions (que Task 4
//     fijó como argumento explícito de ESA función), así que
//     BuildKiroPayload lo lee de un nuevo package var, ToolDescriptionMaxLength
//     (thinking.go) — no de un parámetro propio.
//
// conversationID SÍ conserva el comportamiento extra que pedía el brief más
// allá de la paridad con upstream: si el llamador pasa "", se genera un
// UUIDv4 con crypto/rand (vía internal/utils.NewUUID, Task 8 solo lo
// reutiliza, no lo reimplementa) en vez de mandar una cadena vacía a Kiro.
// Ninguno de los 83 casos ejercita esta rama (conversation_id nunca es "" en
// el corpus, ver docs/CORPUS.md §3), así que no tiene cobertura golden;
// TestBuildKiroPayloadGeneratesConversationIDWhenEmpty la cubre a mano.
//
// El test extrae conversationID de c.Input.kwargs.conversation_id, no de
// c.Output — el brief sugería sacarlo del output grabado porque asumía que
// no era ya un parámetro normal, pero al serlo, sacarlo de kwargs es lo que
// corresponde a una llamada real (kwargs.conversation_id ==
// output.conversationId en los 83 casos, así que el resultado es idéntico).
func TestBuildKiroPayload(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_core/build_kiro_payload")
	if len(cases) != 83 {
		t.Fatalf("se esperaban 83 casos de build_kiro_payload, hay %d", len(cases))
	}

	origEnabled := FakeReasoningEnabled
	origMaxTokens := FakeReasoningMaxTokens
	origBudgetCap := FakeReasoningBudgetCap
	origTruncation := TruncationRecoveryEnabled
	origToolMaxLen := ToolDescriptionMaxLength
	origAutoTrim := AutoTrimPayload
	origMaxBytes := KiroMaxPayloadBytes
	t.Cleanup(func() {
		FakeReasoningEnabled = origEnabled
		FakeReasoningMaxTokens = origMaxTokens
		FakeReasoningBudgetCap = origBudgetCap
		TruncationRecoveryEnabled = origTruncation
		ToolDescriptionMaxLength = origToolMaxLen
		AutoTrimPayload = origAutoTrim
		KiroMaxPayloadBytes = origMaxBytes
	})

	skipped := 0
	for _, c := range cases {
		if reason, known := knownDefectiveBuildKiroPayloadCases[c.Name]; known {
			skipped++
			t.Run(c.Name, func(t *testing.T) {
				t.Skipf("case %s: corpus defectuoso (input aliasing de build_kiro_payload, ver comentario de knownDefectiveBuildKiroPayloadCases): %s", c.Name, reason)
			})
			continue
		}

		t.Run(c.Name, func(t *testing.T) {
			FakeReasoningEnabled = decodeConfigBool(t, c.Input, "FAKE_REASONING_ENABLED", c.Name)
			FakeReasoningMaxTokens = decodeConfigInt(t, c.Input, "FAKE_REASONING_MAX_TOKENS", c.Name)
			FakeReasoningBudgetCap = decodeConfigInt(t, c.Input, "FAKE_REASONING_BUDGET_CAP", c.Name)
			TruncationRecoveryEnabled = decodeConfigBool(t, c.Input, "TRUNCATION_RECOVERY", c.Name)
			ToolDescriptionMaxLength = decodeConfigInt(t, c.Input, "TOOL_DESCRIPTION_MAX_LENGTH", c.Name)
			AutoTrimPayload = decodeConfigBool(t, c.Input, "AUTO_TRIM_PAYLOAD", c.Name)
			KiroMaxPayloadBytes = decodeConfigInt(t, c.Input, "KIRO_MAX_PAYLOAD_BYTES", c.Name)

			messages := decodeMessages(t, testutil.Kwarg(t, c.Input, "messages"), c.Name)

			var systemPrompt string
			if err := json.Unmarshal(testutil.Kwarg(t, c.Input, "system_prompt"), &systemPrompt); err != nil {
				t.Fatalf("case %s: decodificando system_prompt: %v", c.Name, err)
			}

			var modelID string
			if err := json.Unmarshal(testutil.Kwarg(t, c.Input, "model_id"), &modelID); err != nil {
				t.Fatalf("case %s: decodificando model_id: %v", c.Name, err)
			}

			tools := decodeToolsArg(t, c.Input, c.Name)

			var conversationID string
			if err := json.Unmarshal(testutil.Kwarg(t, c.Input, "conversation_id"), &conversationID); err != nil {
				t.Fatalf("case %s: decodificando conversation_id: %v", c.Name, err)
			}

			var profileArn string
			if err := json.Unmarshal(testutil.Kwarg(t, c.Input, "profile_arn"), &profileArn); err != nil {
				t.Fatalf("case %s: decodificando profile_arn: %v", c.Name, err)
			}

			var thinkingCfg ThinkingConfig
			if err := json.Unmarshal(testutil.Kwarg(t, c.Input, "thinking_config"), &thinkingCfg); err != nil {
				t.Fatalf("case %s: decodificando thinking_config: %v", c.Name, err)
			}

			got := BuildKiroPayload(messages, systemPrompt, modelID, tools, conversationID, profileArn, thinkingCfg)

			var want buildKiroPayloadOutput
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("case %s: decodificando output: %v", c.Name, err)
			}

			testutil.AssertJSONEqual(t, got.Payload, want.Payload, c.Name)
			if got.ToolDocumentation != want.ToolDocumentation {
				t.Errorf("case %s: ToolDocumentation:\n got:  %q\n want: %q", c.Name, got.ToolDocumentation, want.ToolDocumentation)
			}
		})
	}

	if skipped != len(knownDefectiveBuildKiroPayloadCases) {
		t.Fatalf("se esperaban %d casos defectuosos documentados, se saltaron %d — knownDefectiveBuildKiroPayloadCases y el corpus se han desincronizado", len(knownDefectiveBuildKiroPayloadCases), skipped)
	}
}

// TestBuildKiroPayloadGeneratesConversationIDWhenEmpty cubre a mano la rama
// de conversationID="" que ningún caso del corpus ejercita (ver el comentario
// de TestBuildKiroPayload): el llamador puede pasar "" y BuildKiroPayload
// genera un UUIDv4 en vez de mandar una cadena vacía a Kiro. No compara
// contra ningún valor grabado — solo la FORMA (36 caracteres, guiones en las
// posiciones 8-13-18-23, como internal/utils.NewUUID ya verifica por su
// cuenta) y que dos llamadas sucesivas no generen el mismo id.
func TestBuildKiroPayloadGeneratesConversationIDWhenEmpty(t *testing.T) {
	msgs := []UnifiedMessage{{Role: "user", Content: "hi"}}

	got1 := BuildKiroPayload(msgs, "", "model", nil, "", "", ThinkingConfig{})
	got2 := BuildKiroPayload(msgs, "", "model", nil, "", "", ThinkingConfig{})

	id1, _ := got1.Payload["conversationState"].(map[string]any)["conversationId"].(string)
	id2, _ := got2.Payload["conversationState"].(map[string]any)["conversationId"].(string)

	if id1 == "" {
		t.Fatalf("conversationId generado está vacío")
	}
	if len(id1) != 36 || id1[8] != '-' || id1[13] != '-' || id1[18] != '-' || id1[23] != '-' {
		t.Fatalf("conversationId generado %q no tiene forma de UUIDv4", id1)
	}
	if id1 == id2 {
		t.Fatalf("dos llamadas sucesivas con conversationID=\"\" generaron el mismo id: %q", id1)
	}
}

// TestBuildKiroPayloadUsesGivenConversationID confirma que un conversationID
// no vacío se usa tal cual, sin generar uno nuevo — la rama que sí cubre el
// corpus (los 83 casos), aquí aislada de la carga de un caso completo para
// que quede como documentación ejecutable del contrato del argumento.
func TestBuildKiroPayloadUsesGivenConversationID(t *testing.T) {
	msgs := []UnifiedMessage{{Role: "user", Content: "hi"}}
	got := BuildKiroPayload(msgs, "", "model", nil, "fixed-id", "", ThinkingConfig{})
	id, _ := got.Payload["conversationState"].(map[string]any)["conversationId"].(string)
	if id != "fixed-id" {
		t.Fatalf("conversationId = %q, want %q", id, "fixed-id")
	}
}
