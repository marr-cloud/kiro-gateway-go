// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"bytes"
	"testing"
)

// TestTrimPayloadUnderThreshold verifica que un payload bajo el umbral no se
// modifique. Test scenario 1: payload bajo el umbral -> sin cambios.
func TestTrimPayloadUnderThreshold(t *testing.T) {
	// Crear un payload simple bajo el umbral
	payload := map[string]any{
		"conversationState": map[string]any{
			"chatTriggerType": "MANUAL",
			"conversationId":  "test-id",
			"history": []map[string]any{
				{
					"userInputMessage": map[string]any{
						"content": "Hello",
						"modelId": "test",
						"origin":  "AI_EDITOR",
					},
				},
			},
			"currentMessage": map[string]any{
				"userInputMessage": map[string]any{
					"content": "Hi there",
					"modelId": "test",
					"origin":  "AI_EDITOR",
				},
			},
		},
	}

	// Guardar estado original
	originalHistory := payload["conversationState"].(map[string]any)["history"].([]map[string]any)
	originalLen := len(originalHistory)
	originalSize := checkPayloadSize(payload)

	// Trimear con límite muy alto
	TrimPayloadToLimit(payload, 1000000)

	// Verificar que no cambió
	finalHistory := payload["conversationState"].(map[string]any)["history"].([]map[string]any)
	if len(finalHistory) != originalLen {
		t.Errorf("expected %d history entries, got %d", originalLen, len(finalHistory))
	}

	// Verificar que el tamaño no cambió
	finalSize := checkPayloadSize(payload)
	if finalSize != originalSize {
		t.Errorf("payload size changed from %d to %d", originalSize, finalSize)
	}
}

// TestTrimPayloadOverThresholdWithTrimming verifica que un payload que supera
// el umbral sea recortado cuando AutoTrimPayload=true. Test scenario 2: payload
// sobre el umbral con AutoTrimPayload=true -> recortado.
func TestTrimPayloadOverThresholdWithTrimming(t *testing.T) {
	// Guardar el valor original de AutoTrimPayload
	origAutoTrim := AutoTrimPayload
	origMaxBytes := KiroMaxPayloadBytes
	defer func() {
		AutoTrimPayload = origAutoTrim
		KiroMaxPayloadBytes = origMaxBytes
	}()

	AutoTrimPayload = true
	KiroMaxPayloadBytes = 500 // Establecer un límite muy bajo para forzar trimming

	// Crear un payload con varias entradas de historial
	payload := map[string]any{
		"conversationState": map[string]any{
			"chatTriggerType": "MANUAL",
			"conversationId":  "test-id",
			"history": []map[string]any{
				{
					"userInputMessage": map[string]any{
						"content": "First message from user",
						"modelId": "test",
						"origin":  "AI_EDITOR",
					},
				},
				{
					"assistantResponseMessage": map[string]any{
						"content": "First response from assistant",
					},
				},
				{
					"userInputMessage": map[string]any{
						"content": "Second message from user",
						"modelId": "test",
						"origin":  "AI_EDITOR",
					},
				},
				{
					"assistantResponseMessage": map[string]any{
						"content": "Second response from assistant",
					},
				},
				{
					"userInputMessage": map[string]any{
						"content": "Third message from user",
						"modelId": "test",
						"origin":  "AI_EDITOR",
					},
				},
				{
					"assistantResponseMessage": map[string]any{
						"content": "Third response from assistant",
					},
				},
			},
			"currentMessage": map[string]any{
				"userInputMessage": map[string]any{
					"content": "Current message",
					"modelId": "test",
					"origin":  "AI_EDITOR",
				},
			},
		},
	}

	originalHistoryLen := len(payload["conversationState"].(map[string]any)["history"].([]map[string]any))

	// Aplicar trimming
	TrimPayloadToLimit(payload, KiroMaxPayloadBytes)

	finalSize := checkPayloadSize(payload)
	finalHistoryLen := len(payload["conversationState"].(map[string]any)["history"].([]map[string]any))

	// Verificar que el tamaño final es menor o igual al límite
	if finalSize > KiroMaxPayloadBytes {
		t.Errorf("final payload size %d exceeds limit %d", finalSize, KiroMaxPayloadBytes)
	}

	// Verificar que hubo trimming (menos entradas)
	if finalHistoryLen >= originalHistoryLen {
		t.Errorf("expected history to be trimmed from %d entries, but got %d entries",
			originalHistoryLen, finalHistoryLen)
	}

	// Verificar que siempre hay al menos 2 entradas si las había originalmente
	if originalHistoryLen > 2 && finalHistoryLen < 2 {
		t.Errorf("trimming should preserve at least 2 history entries, got %d", finalHistoryLen)
	}
}

// TestAlignmentPersistedInPayload verifica que la alineación a userInputMessage
// persiste en el payload (CRITICAL FIX: alignment debe mutar el payload real, no
// solo una variable local).
func TestAlignmentPersistedInPayload(t *testing.T) {
	origAutoTrim := AutoTrimPayload
	origMaxBytes := KiroMaxPayloadBytes
	defer func() {
		AutoTrimPayload = origAutoTrim
		KiroMaxPayloadBytes = origMaxBytes
	}()

	AutoTrimPayload = true
	KiroMaxPayloadBytes = 500

	// Crear un payload cuyo historial, después del trimming, comienza con una
	// entrada NO-userInputMessage (solo assistantResponseMessage).
	// Después del trim y alignment, esa entrada debe ser removida del payload.
	payload := map[string]any{
		"conversationState": map[string]any{
			"chatTriggerType": "MANUAL",
			"conversationId":  "test-id",
			"history": []map[string]any{
				{
					"userInputMessage": map[string]any{
						"content": "Message 1",
						"modelId": "test",
						"origin":  "AI_EDITOR",
					},
				},
				{
					"assistantResponseMessage": map[string]any{
						"content": "Response 1",
					},
				},
				{
					"userInputMessage": map[string]any{
						"content": "Message 2",
						"modelId": "test",
						"origin":  "AI_EDITOR",
					},
				},
				{
					"assistantResponseMessage": map[string]any{
						"content": "Response 2",
					},
				},
				// Esta entrada no tiene userInputMessage - será left-over después
				// del trim y debe ser removida por alignment.
				{
					"assistantResponseMessage": map[string]any{
						"content": "Orphaned assistant message",
					},
				},
			},
			"currentMessage": map[string]any{
				"userInputMessage": map[string]any{
					"content": "Current",
					"modelId": "test",
					"origin":  "AI_EDITOR",
				},
			},
		},
	}

	// Aplicar trimming
	TrimPayloadToLimit(payload, KiroMaxPayloadBytes)

	// Obtener el historial final del payload
	conversationState := payload["conversationState"].(map[string]any)
	historyRaw, hasHistory := conversationState["history"]
	if !hasHistory {
		// Si la historia fue eliminada (porque quedó vacía), eso está bien
		return
	}

	finalHistory, ok := historyRaw.([]map[string]any)
	if !ok {
		t.Fatalf("history has wrong type: %T", historyRaw)
	}

	// CRITICAL: Verificar que el primer entry tiene userInputMessage
	// (i.e., la alineación fue aplicada y persiste en el payload)
	if len(finalHistory) > 0 {
		if _, hasUserInputMessage := finalHistory[0]["userInputMessage"]; !hasUserInputMessage {
			t.Errorf("CRITICAL: alignment failed - first history entry does not have userInputMessage. "+
				"This proves alignment was not persisted in the payload. Entry: %v", finalHistory[0])
		}
	}
}

// TestRepairOrphanedToolResults verifica que la reparación de toolResults
// huérfanos funciona correctamente.
func TestRepairOrphanedToolResults(t *testing.T) {
	history := []map[string]any{
		{
			"userInputMessage": map[string]any{
				"content": "Initial message",
			},
		},
		{
			"assistantResponseMessage": map[string]any{
				"content": "Response",
				"toolUses": []map[string]any{
					{
						"toolUseId": "tool-1",
						"name":      "calculator",
					},
				},
			},
		},
		{
			"userInputMessage": map[string]any{
				"content": "Follow-up",
				"userInputMessageContext": map[string]any{
					"toolResults": []map[string]any{
						{
							"toolUseId": "tool-1",
							"content":   "2 + 2 = 4",
						},
						{
							"toolUseId": "orphaned-tool",
							"content":   "Orphaned result",
						},
					},
				},
			},
		},
	}

	repairOrphanedToolResults(history)

	userCtx := history[2]["userInputMessage"].(map[string]any)["userInputMessageContext"].(map[string]any)
	toolResults := userCtx["toolResults"].([]map[string]any)

	// Solo debe quedar tool-1 (tool-2 era huérfano)
	if len(toolResults) != 1 {
		t.Errorf("expected 1 kept tool result, got %d", len(toolResults))
	}

	if toolResults[0]["toolUseId"] != "tool-1" {
		t.Errorf("expected tool-1 to be kept, got %s", toolResults[0]["toolUseId"])
	}

	// Verificar que el contenido huérfano se añadió al mensaje
	content := history[2]["userInputMessage"].(map[string]any)["content"].(string)
	if !bytes.Contains([]byte(content), []byte("[trimmed tool result]")) {
		t.Errorf("expected orphaned tool result marker in content, got: %s", content)
	}
}

// TestEmptyHistory verifica que un payload vacío no causa pánico.
func TestEmptyHistory(t *testing.T) {
	payload := map[string]any{
		"conversationState": map[string]any{
			"chatTriggerType": "MANUAL",
			"conversationId":  "test-id",
			"currentMessage": map[string]any{
				"userInputMessage": map[string]any{
					"content": "Hello",
					"modelId": "test",
					"origin":  "AI_EDITOR",
				},
			},
		},
	}

	// No debe causar pánico
	TrimPayloadToLimit(payload, 100)

	// Verificar que sigue siendo válido
	if payload["conversationState"] == nil {
		t.Errorf("conversationState should not be nil")
	}
}

// TestCheckPayloadSize verifica que la medición de tamaño es correcta.
func TestCheckPayloadSize(t *testing.T) {
	payload := map[string]any{
		"test": "value",
	}

	size := checkPayloadSize(payload)
	if size <= 0 {
		t.Errorf("expected positive size, got %d", size)
	}

	// Verificar que el tamaño es reproducible
	size2 := checkPayloadSize(payload)
	if size != size2 {
		t.Errorf("payload size not reproducible: %d vs %d", size, size2)
	}
}
