// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// decodeMessages decodifica una lista de UnifiedMessage tal como la graba el
// corpus (objetos JSON con claves snake_case: role, content, tool_calls,
// tool_results, images) usando los tags json de UnifiedMessage.
func decodeMessages(t testing.TB, raw json.RawMessage, caseName string) []UnifiedMessage {
	t.Helper()
	var msgs []UnifiedMessage
	if err := json.Unmarshal(raw, &msgs); err != nil {
		t.Fatalf("case %s: decode messages: %v", caseName, err)
	}
	return msgs
}

// messagesToRaw convierte una lista de UnifiedMessage a la forma
// []map[string]any que usa el corpus para sus mensajes: los cinco campos
// SIEMPRE presentes (role, content, tool_calls, tool_results, images), con
// null explícito en vez de ausencia cuando un slice está vacío. No se
// reutiliza json.Marshal(msgs) directamente porque UnifiedMessage lleva
// omitempty en los tres slices opcionales (ver types.go): con eso Marshal
// OMITIRÍA la clave en vez de escribir null, y el corpus siempre escribe la
// clave. Este helper reconstruye la forma exacta del corpus para que
// testutil.AssertJSONEqual compare lo mismo a ambos lados.
func messagesToRaw(msgs []UnifiedMessage) []map[string]any {
	out := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		out[i] = map[string]any{
			"role":         m.Role,
			"content":      m.Content,
			"tool_calls":   m.ToolCalls,
			"tool_results": m.ToolResults,
			"images":       m.Images,
		}
	}
	return out
}

// knownCorpusInputAliasingDefects son los 10 casos de
// testdata/converters_core/merge_adjacent_messages cuyo campo "input"
// grabado NO refleja el estado real anterior a la llamada.
//
// Causa raíz (confirmada ejecutando kiro.converters_core.merge_adjacent_messages
// tal cual, en el commit fijado a5292ca, contra el "input" grabado de estos
// casos concretos: no reproduce el "output" grabado; SÍ lo reproduce contra
// los otros 31): tools/corpus/recorder.py:_wrap_function llama a la función
// original y SOLO DESPUÉS serializa `args` para grabarlo como "input":
//
//	result = original(*args, **kwargs)
//	_record_function(target, args, kwargs, result, config)
//
// merge_adjacent_messages muta los UnifiedMessage en sitio (reasigna
// last.content/last.tool_calls/last.tool_results sobre el objeto que ya
// está en `merged`, que es el MISMO objeto que sigue vivio en `args[0]`
// porque Python pasa la lista por referencia). Cuando se fusionan dos o más
// mensajes, el elemento superviviente de `args[0]` queda mutado al estado ya
// fusionado ANTES de que el grabador lo serialice como "input"; el resto de
// elementos fusionados (los que no sobreviven como `last`) se serializan sin
// mutar, porque la función solo los lee. El resultado es que el "input"
// grabado no es la entrada real de ninguna llamada: mezcla el estado
// post-fusión de un mensaje con el estado pre-fusión de otro.
//
// Consecuencia práctica: un port fiel de merge_adjacent_messages (el que
// pide la tarea, y el que hace falta para que Task 8 encadene las cinco
// funciones sobre entradas reales) NO puede reproducir el "output" de estos
// 10 casos partiendo de su "input" grabado, porque ese "input" nunca ocurrió
// como tal. Se confirmó ejecutando la función original contra el "input" de
// los 41 casos: 31 coinciden con el "output" grabado, exactamente estos 10
// no. Ver el informe de la tarea para la reproducción completa con un caso
// concreto (06cb0407e2ca7547: fusión de dos mensajes assistant con
// tool_calls duplicados).
//
// Los otros cuatro objetivos de este fichero (ensure_assistant_before_tool_results,
// ensure_first_message_is_user, normalize_message_roles,
// ensure_alternating_roles) construyen SIEMPRE un UnifiedMessage nuevo en
// vez de mutar el que reciben, así que no sufren este defecto: los 33+40+42+43
// casos de esos cuatro objetivos se verificaron uno a uno contra la función
// original y los 158 coinciden.
var knownCorpusInputAliasingDefects = map[string]string{
	"06cb0407e2ca7547": "fusión de 2 assistant con tool_calls duplicados: input[0] queda mutado al estado ya fusionado",
	"07595db32f8eff83": "fusión de 2 user con tool_results duplicados: mismo defecto de aliasing",
	"3f0f14ef66f08f71": "fusión de 2 user (\"Hello\"+\"World\"): input[0] ya trae el contenido fusionado",
	"9195a6a11d8cc1a2": "fusión de assistant con tool_calls: mismo defecto de aliasing",
	"a083c350718d3cec": "fusión de 2 user con content en forma de lista de bloques: mismo defecto",
	"a70774077263884f": "fusión de assistant con tool_calls: mismo defecto de aliasing",
	"b0fe4582c5f8587e": "cadena de 5 mensajes con dos fusiones (user+user, assistant+assistant): mismo defecto en ambas",
	"ca12320330f2af70": "fusión de 2 user: input[0] ya trae el contenido fusionado",
	"d134ee1ee6418641": "fusión de 2 user con tool_results: mismo defecto de aliasing",
	"e8aa05c5f4a493fc": "fusión de assistant con tool_calls: mismo defecto de aliasing",
}

// TestEnsureAssistantBeforeToolResultsAgainstCorpus valida
// EnsureAssistantBeforeToolResults contra los 33 casos grabados de
// kiro.converters_core:ensure_assistant_before_tool_results. La salida
// grabada es la tupla de Python [messages, modified] como array JSON de dos
// elementos.
func TestEnsureAssistantBeforeToolResultsAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_core/ensure_assistant_before_tool_results") {
		t.Run(c.Name, func(t *testing.T) {
			input := decodeMessages(t, testutil.Arg(t, c.Input, 0), c.Name)

			var tuple [2]json.RawMessage
			if err := json.Unmarshal(c.Output, &tuple); err != nil {
				t.Fatalf("case %s: decode output tuple: %v", c.Name, err)
			}
			var wantModified bool
			if err := json.Unmarshal(tuple[1], &wantModified); err != nil {
				t.Fatalf("case %s: decode output[1] (modified): %v", c.Name, err)
			}

			gotMsgs, gotModified := EnsureAssistantBeforeToolResults(input)

			testutil.AssertJSONEqual(t, messagesToRaw(gotMsgs), tuple[0], c.Name)
			if gotModified != wantModified {
				t.Fatalf("case %s: modified = %v, want %v", c.Name, gotModified, wantModified)
			}
		})
	}
}

// TestMergeAdjacentMessagesAgainstCorpus valida MergeAdjacentMessages contra
// los 41 casos grabados de kiro.converters_core:merge_adjacent_messages.
// Diez de esos 41 casos se saltan documentadamente: ver
// knownCorpusInputAliasingDefects.
func TestMergeAdjacentMessagesAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_core/merge_adjacent_messages") {
		t.Run(c.Name, func(t *testing.T) {
			if reason, skip := knownCorpusInputAliasingDefects[c.Name]; skip {
				t.Skipf("case %s: input grabado corrupto por aliasing del grabador (ver knownCorpusInputAliasingDefects): %s", c.Name, reason)
			}

			input := decodeMessages(t, testutil.Arg(t, c.Input, 0), c.Name)
			got := MergeAdjacentMessages(input)
			testutil.AssertJSONEqual(t, messagesToRaw(got), c.Output, c.Name)
		})
	}
}

// TestEnsureFirstMessageIsUserAgainstCorpus valida EnsureFirstMessageIsUser
// contra los 40 casos grabados de
// kiro.converters_core:ensure_first_message_is_user.
func TestEnsureFirstMessageIsUserAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_core/ensure_first_message_is_user") {
		t.Run(c.Name, func(t *testing.T) {
			input := decodeMessages(t, testutil.Arg(t, c.Input, 0), c.Name)
			got := EnsureFirstMessageIsUser(input)
			testutil.AssertJSONEqual(t, messagesToRaw(got), c.Output, c.Name)
		})
	}
}

// TestNormalizeMessageRolesAgainstCorpus valida NormalizeMessageRoles contra
// los 42 casos grabados de kiro.converters_core:normalize_message_roles.
func TestNormalizeMessageRolesAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_core/normalize_message_roles") {
		t.Run(c.Name, func(t *testing.T) {
			input := decodeMessages(t, testutil.Arg(t, c.Input, 0), c.Name)
			got := NormalizeMessageRoles(input)
			testutil.AssertJSONEqual(t, messagesToRaw(got), c.Output, c.Name)
		})
	}
}

// TestEnsureAlternatingRolesAgainstCorpus valida EnsureAlternatingRoles
// contra los 43 casos grabados de kiro.converters_core:ensure_alternating_roles.
func TestEnsureAlternatingRolesAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_core/ensure_alternating_roles") {
		t.Run(c.Name, func(t *testing.T) {
			input := decodeMessages(t, testutil.Arg(t, c.Input, 0), c.Name)
			got := EnsureAlternatingRoles(input)
			testutil.AssertJSONEqual(t, messagesToRaw(got), c.Output, c.Name)
		})
	}
}
