// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package server

import (
	"net/http"
	"path/filepath"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelcaps"
	"github.com/marr-cloud/kiro-gateway-go/internal/version"
)

// kiroStatusResponse es el JSON de GET /kiro/status: lo que el mod de Claude
// Code (claude-mod/) necesita para /kiro y para reintentar un corte con el
// modelo de respaldo. Sin equivalente en el original (DIFFERENCES §20). No
// sale de la máquina: lee lo que el discovery ya guardó.
type kiroStatusResponse struct {
	Version       string            `json:"version"`
	UptimeSeconds float64           `json:"uptime_seconds"`
	ActiveAccount *string           `json:"active_account"`
	Debug         kiroStatusDebug   `json:"debug"`
	Models        []kiroStatusModel `json:"models"`
}

type kiroStatusDebug struct {
	Mode string `json:"mode"`
	Dir  string `json:"dir"` // absoluto, resuelto desde el directorio de trabajo
}

type kiroStatusModel struct {
	ID              string   `json:"id"`
	NativeThinking  []string `json:"native_thinking"`
	EffortLevels    []string `json:"effort_levels"`
	RefusalFallback string   `json:"refusal_fallback"`
}

func (s *Server) handleKiroStatus(w http.ResponseWriter, r *http.Request) {
	dir, err := filepath.Abs(s.cfg.DebugDir)
	if err != nil {
		dir = s.cfg.DebugDir
	}
	ids := s.availableModels()
	models := make([]kiroStatusModel, 0, len(ids))
	for _, id := range ids {
		caps, _ := modelcaps.Get(id)
		models = append(models, kiroStatusModel{
			ID:              id,
			NativeThinking:  orEmpty(caps.ThinkingTypes),
			EffortLevels:    orEmpty(caps.EffortLevels),
			RefusalFallback: modelcaps.RefusalFallback(id),
		})
	}
	writeJSON(w, http.StatusOK, kiroStatusResponse{
		Version:       version.Version(),
		UptimeSeconds: s.uptimeSeconds(),
		ActiveAccount: s.activeAccountID(),
		Debug:         kiroStatusDebug{Mode: s.cfg.DebugMode, Dir: dir},
		Models:        models,
	})
}

// orEmpty devuelve [] en vez de nil para que el JSON lleve una lista.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
