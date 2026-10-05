// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package modelcaps guarda qué razonamiento nativo admite cada modelo de Kiro
// y traduce lo que pide el cliente (thinking, effort) a los
// additionalModelRequestFields de GenerateAssistantResponse. Sin equivalente
// en el original (DIFFERENCES §18).
//
// Las capacidades salen del additionalModelRequestFieldsSchema que devuelve
// ListAvailableModels por modelo, igual que hace el IDE de Kiro: thinking.type
// (p.ej. adaptive/disabled) y el nivel de esfuerzo en output_config.effort
// (Claude) o reasoning.effort (GPT).
package modelcaps

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelresolver"
)

// Caps son las capacidades de razonamiento nativo de un modelo.
type Caps struct {
	ThinkingTypes    []string // enum de thinking.type
	ThinkingDisplays []string // enum de thinking.display
	EffortPath       string   // "output_config", "reasoning" o ""
	EffortLevels     []string // enum de <EffortPath>.effort
}

// Request es lo que pide el cliente, ya extraído de su dialecto. Thinking es
// "adaptive", "enabled", "disabled" o "" (sin especificar).
type Request struct {
	Thinking string
	Display  string
	Effort   string
}

type enumSchema struct {
	Enum []string `json:"enum"`
}

type schema struct {
	Properties struct {
		Thinking struct {
			Properties struct {
				Type    enumSchema `json:"type"`
				Display enumSchema `json:"display"`
			} `json:"properties"`
		} `json:"thinking"`
		OutputConfig struct {
			Properties struct {
				Effort enumSchema `json:"effort"`
			} `json:"properties"`
		} `json:"output_config"`
		Reasoning struct {
			Properties struct {
				Effort enumSchema `json:"effort"`
			} `json:"properties"`
		} `json:"reasoning"`
	} `json:"properties"`
}

// ParseSchema extrae Caps de un additionalModelRequestFieldsSchema. ok=false
// si el modelo no admite ni thinking ni esfuerzo.
func ParseSchema(raw json.RawMessage) (Caps, bool) {
	var s schema
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return Caps{}, false
	}
	var c Caps
	c.ThinkingTypes = s.Properties.Thinking.Properties.Type.Enum
	c.ThinkingDisplays = s.Properties.Thinking.Properties.Display.Enum
	switch {
	case len(s.Properties.OutputConfig.Properties.Effort.Enum) > 0:
		c.EffortPath, c.EffortLevels = "output_config", s.Properties.OutputConfig.Properties.Effort.Enum
	case len(s.Properties.Reasoning.Properties.Effort.Enum) > 0:
		c.EffortPath, c.EffortLevels = "reasoning", s.Properties.Reasoning.Properties.Effort.Enum
	}
	if len(c.ThinkingTypes) == 0 && c.EffortPath == "" {
		return Caps{}, false
	}
	return c, true
}

// cappedWithoutThinking son los niveles que el IDE de Kiro no manda con el
// thinking desactivado.
var cappedWithoutThinking = []string{"xhigh", "max"}

// RequestFields construye los additionalModelRequestFields para caps y req, o
// nil si no hay nada que mandar. Nunca manda un valor que el esquema del
// modelo no admita: lo omite y Kiro aplica su default.
func RequestFields(caps Caps, req Request) map[string]any {
	out := map[string]any{}
	thinking := req.Thinking
	if thinking == "enabled" {
		thinking = "adaptive" // el budget_tokens de los clientes viejos no existe en Kiro
	}

	effort := req.Effort
	if caps.EffortPath == "reasoning" && effort == "" && thinking == "disabled" {
		effort = "none"
	}
	if thinking == "disabled" && slices.Contains(caps.ThinkingTypes, "disabled") && slices.Contains(cappedWithoutThinking, effort) {
		effort = highestUncapped(caps.EffortLevels)
	}
	if effort != "" && slices.Contains(caps.EffortLevels, effort) {
		out[caps.EffortPath] = map[string]any{"effort": effort}
	}

	if thinking != "" && slices.Contains(caps.ThinkingTypes, thinking) {
		t := map[string]any{"type": thinking}
		if req.Display != "" && slices.Contains(caps.ThinkingDisplays, req.Display) {
			t["display"] = req.Display
		}
		out["thinking"] = t
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

func highestUncapped(levels []string) string {
	for i := len(levels) - 1; i >= 0; i-- {
		if !slices.Contains(cappedWithoutThinking, levels[i]) {
			return levels[i]
		}
	}
	return ""
}

var (
	mu        sync.RWMutex
	registry  = map[string]Caps{}
	fallbacks = map[string]string{}
)

func key(modelID string) string {
	return strings.ToLower(modelresolver.NormalizeModelName(strings.TrimSpace(modelID)))
}

// Set registra las capacidades de modelID (lo llama el discovery de modelos).
func Set(modelID string, caps Caps) {
	mu.Lock()
	defer mu.Unlock()
	registry[key(modelID)] = caps
}

// Get devuelve las capacidades de modelID, normalizando el id igual que las
// peticiones (claude-sonnet-4-6 equivale a claude-sonnet-4.6).
func Get(modelID string) (Caps, bool) {
	mu.RLock()
	defer mu.RUnlock()
	c, ok := registry[key(modelID)]
	return c, ok
}

// SetRefusalFallback registra el modelo con el que Kiro manda reintentar
// cuando modelID corta la respuesta (refusalFallbackModels de
// ListAvailableModels). Lo lee GET /kiro/status para el mod de Claude Code
// (DIFFERENCES §20).
func SetRefusalFallback(modelID, fallback string) {
	mu.Lock()
	defer mu.Unlock()
	fallbacks[key(modelID)] = fallback
}

// RefusalFallback devuelve el modelo de respaldo de modelID, o "" si Kiro no
// declaró ninguno. Normaliza el id igual que Get.
func RefusalFallback(modelID string) string {
	mu.RLock()
	defer mu.RUnlock()
	return fallbacks[key(modelID)]
}

// Reset vacía el registro (tests).
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	registry = map[string]Caps{}
	fallbacks = map[string]string{}
}
