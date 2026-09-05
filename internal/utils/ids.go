// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Punto de inyección de identificadores.
//
// El original construye cuatro identificadores a partir de uuid.uuid4().hex
// con recortes de longitud distintos:
//
//	chatcmpl-{uuid4().hex}       ← utils.py :: generate_completion_id
//	call_{uuid4().hex[:8]}       ← utils.py :: generate_tool_call_id
//	msg_{uuid4().hex[:24]}       ← streaming_anthropic.py :: generate_message_id
//	sig_{uuid4().hex[:32]}       ← streaming_anthropic.py :: generate_thinking_signature
//
// Y usa str(uuid.uuid4()) suelto para la cabecera amz-sdk-invocation-id y la
// rama de respaldo de generate_conversation_id.
//
// Contrato de congelación (docs/CORPUS.md §4). Fase 1 registró el corpus
// sustituyendo los generadores por contadores globales deterministas:
//
//	chatcmpl-{n:032x}   |   call_{n:08x}   |   msg_{n:024x}   |   sig_{n:032x}
//	uuid4() → UUID(int=(n << 96) | n, version=4)
//
// Las fases 4 y 5 comparan las salidas del port contra ese corpus, así que
// necesitan el mismo punto de inyección: sustituyen NewHexToken y NewUUID por
// versiones deterministas y verifican los identificadores byte a byte.
// Producción usa crypto/rand a través de defaultNewHexToken / defaultNewUUID;
// los tests reasignan las variables al principio del test y restauran en un
// t.Cleanup.
var (
	// NewHexToken devuelve 32 caracteres hex en minúscula, equivalente a
	// uuid.uuid4().hex en Python. Los cuatro generadores basados en hex
	// pasan por esta variable, así que un único seam los cubre todos.
	NewHexToken = defaultNewHexToken

	// NewUUID devuelve un UUIDv4 en formato canónico 8-4-4-4-12 en
	// minúscula (36 caracteres), equivalente a str(uuid.uuid4()).
	NewUUID = defaultNewUUID
)

func defaultNewHexToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand solo falla si el SO no entrega entropía; en ese
		// caso el proceso no puede servir peticiones. Aborta antes de
		// devolver un identificador silenciosamente predecible.
		panic(fmt.Errorf("utils: crypto/rand falló: %w", err))
	}
	// Bits de versión (4) y variante (RFC 4122) como uuid.uuid4().
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:])
}

func defaultNewUUID() string {
	h := defaultNewHexToken()
	// 8-4-4-4-12 clásico.
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// GenerateCompletionID devuelve un identificador con formato
// "chatcmpl-<32 hex>", el que espera el cliente compatible con OpenAI. Paridad
// con utils.py :: generate_completion_id.
func GenerateCompletionID() string { return "chatcmpl-" + NewHexToken() }

// GenerateToolCallID devuelve "call_<8 hex>". Paridad con
// utils.py :: generate_tool_call_id.
func GenerateToolCallID() string { return "call_" + NewHexToken()[:8] }

// GenerateMessageID devuelve "msg_<24 hex>", el formato de Anthropic. Paridad
// con streaming_anthropic.py :: generate_message_id.
func GenerateMessageID() string { return "msg_" + NewHexToken()[:24] }

// GenerateThinkingSignature devuelve "sig_<32 hex>", placeholder de firma
// para los bloques de "thinking". Paridad con
// streaming_anthropic.py :: generate_thinking_signature.
func GenerateThinkingSignature() string { return "sig_" + NewHexToken()[:32] }

// GenerateConversationID devuelve un identificador estable calculado a partir
// del historial de mensajes, o un uuid4 nuevo si no hay mensajes.
//
// Se usa para persistir estado de recuperación por truncamiento entre
// peticiones de la misma conversación: dos peticiones sucesivas con el mismo
// historial deben producir el mismo id.
//
// Algoritmo (portado de utils.py :: generate_conversation_id):
//
//  1. Si len(messages) == 0, devuelve NewUUID() (rama de respaldo).
//  2. Toma los tres primeros mensajes y el último; si len ≤ 3, todos.
//  3. Para cada mensaje simplifica a {"role": <str>, "content": <str>} con:
//     - role: el valor de la clave "role" o "unknown" si falta.
//     - content: los primeros 100 caracteres del contenido. Si el contenido
//     es una lista (bloques al estilo Anthropic) se pasa por json.Marshal
//     antes de recortar.
//  4. sha256(json.Marshal(simplified)) → primeros 16 hex.
//
// El corpus solo cubre la rama de respaldo (docs/CORPUS.md §5): los cuatro
// call sites del original la llaman sin mensajes, así que la rama del hash no
// tiene cobertura golden y hay que verificarla con tests a mano. Además, este
// id nunca cruza la red hacia Kiro ni hacia el cliente: es interno al
// gateway. No es necesario que coincida byte a byte con el hash de Python,
// solo que sea estable dentro del port.
func GenerateConversationID(messages []map[string]any) string {
	if len(messages) == 0 {
		return NewUUID()
	}

	key := messages
	if len(messages) > 3 {
		key = make([]map[string]any, 0, 4)
		key = append(key, messages[:3]...)
		key = append(key, messages[len(messages)-1])
	}

	simplified := make([]map[string]string, 0, len(key))
	for _, m := range key {
		role := "unknown"
		if v, ok := m["role"]; ok {
			if s, isStr := v.(string); isStr {
				role = s
			} else {
				role = fmt.Sprint(v)
			}
		}

		var content string
		if v, ok := m["content"]; ok {
			switch c := v.(type) {
			case string:
				content = truncateRunes(c, 100)
			case []any:
				// Bloques Anthropic. Go ordena las claves de map en
				// json.Marshal, como json.dumps(sort_keys=True).
				raw, err := json.Marshal(c)
				if err != nil {
					content = truncateRunes(fmt.Sprint(c), 100)
				} else {
					content = truncateRunes(string(raw), 100)
				}
			default:
				content = truncateRunes(fmt.Sprint(c), 100)
			}
		}

		simplified = append(simplified, map[string]string{
			"role":    role,
			"content": content,
		})
	}

	raw, err := json.Marshal(simplified)
	if err != nil {
		// map[string]string nunca falla al codificar. Defensivo.
		return NewUUID()
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:16]
}

// truncateRunes trunca s a n runas (no bytes). Python 3 opera sobre code
// points; los 100 primeros caracteres de "áéíóú" son cinco caracteres, no
// diez bytes.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
