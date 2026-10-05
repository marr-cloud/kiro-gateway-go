// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streaminganthropic

import (
	"encoding/json"
	"io"
)

// Razonamiento nativo de Kiro (reasoningContentEvent) como bloques thinking
// de Anthropic con la firma real del modelo, para que el cliente los
// devuelva en el historial. Sin equivalente en el original (DIFFERENCES §18).
// El bloque abre con signature "" y la firma llega al final en un
// signature_delta, igual que en la API de Anthropic.

// handleNativeThinking emite un trozo de razonamiento nativo. Fuera de
// AsReasoningContent se trata igual que el fake reasoning (texto o nada).
func (f *Formatter) handleNativeThinking(text string, w io.Writer) error {
	if f.thinkingHandling != AsReasoningContent {
		return f.handleThinking(text, w)
	}
	f.fullThinkingContent += text
	if err := f.openNativeThinking(w); err != nil {
		return err
	}
	return f.emitThinkingDelta(f.blocks.ThinkingIndex(), text, w)
}

// handleThinkingSignature cierra el bloque thinking con su firma. Si no había
// texto (razonamiento oculto), emite un bloque thinking vacío firmado.
func (f *Formatter) handleThinkingSignature(signature string, w io.Writer) error {
	if f.thinkingHandling != AsReasoningContent {
		return nil
	}
	if err := f.openNativeThinking(w); err != nil {
		return err
	}
	idx := f.blocks.ThinkingIndex()
	inner, err := json.Marshal(signatureDeltaInner{Type: "signature_delta", Signature: signature})
	if err != nil {
		return err
	}
	if err := writeEvent(w, "content_block_delta", contentBlockDeltaData{Type: "content_block_delta", Index: idx, Delta: inner}); err != nil {
		return err
	}
	f.blocks.CloseThinking()
	return f.emitBlockStop(idx, w)
}

// handleRedactedThinking emite un bloque redacted_thinking completo.
func (f *Formatter) handleRedactedThinking(data string, w io.Writer) error {
	if f.thinkingHandling != AsReasoningContent {
		return nil
	}
	if err := f.closeOpenBlocks(w); err != nil {
		return err
	}
	idx := f.blocks.ReserveToolBlock()
	inner, err := json.Marshal(redactedThinkingContentBlock{Type: "redacted_thinking", Data: data})
	if err != nil {
		return err
	}
	if err := writeEvent(w, "content_block_start", contentBlockStartData{Type: "content_block_start", Index: idx, ContentBlock: inner}); err != nil {
		return err
	}
	return f.emitBlockStop(idx, w)
}

// openNativeThinking abre un bloque thinking sin firma si no hay uno abierto,
// cerrando antes el texto abierto (el thinking intercalado puede llegar
// después de texto).
func (f *Formatter) openNativeThinking(w io.Writer) error {
	if f.blocks.ThinkingOpen() {
		return nil
	}
	if idx, closed := f.blocks.CloseText(); closed {
		if err := f.emitBlockStop(idx, w); err != nil {
			return err
		}
	}
	idx, _ := f.blocks.OpenThinking()
	inner, err := json.Marshal(thinkingContentBlock{Type: "thinking", Thinking: "", Signature: ""})
	if err != nil {
		return err
	}
	return writeEvent(w, "content_block_start", contentBlockStartData{Type: "content_block_start", Index: idx, ContentBlock: inner})
}

func (f *Formatter) closeOpenBlocks(w io.Writer) error {
	if idx, closed := f.blocks.CloseThinking(); closed {
		if err := f.emitBlockStop(idx, w); err != nil {
			return err
		}
	}
	if idx, closed := f.blocks.CloseText(); closed {
		return f.emitBlockStop(idx, w)
	}
	return nil
}

type signatureDeltaInner struct {
	Type      string `json:"type"`
	Signature string `json:"signature"`
}

type redactedThinkingContentBlock struct {
	Type string `json:"type"`
	Data string `json:"data"`
}
