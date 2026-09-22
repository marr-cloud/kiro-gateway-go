// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Descubrimiento dinámico de modelos contra management.<region>.kiro.dev, el
// endpoint que usa el Kiro CLI actual para ListAvailableModels. Es una
// DIVERGENCIA deliberada del upstream (que no conoce ese host; ver
// DIFFERENCES §12): no toca el wire de conversaciones, solo la lista que
// devuelve /v1/models. Se construye a mano (AWS JSON 1.0: POST + X-Amz-Target)
// en vez de pasar por httpclient.RequestWithRetry, que fuerza el X-Amz-Target
// de GenerateAssistantResponse (utils.GetKiroHeaders) — mismo motivo por el que
// internal/mcptools/client.go arma su propia petición.
package accountmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// managementModelsTimeout acota cada ciclo de descubrimiento (incluida la
// paginación por nextToken).
const managementModelsTimeout = 30 * time.Second

// listModelsFromManagement devuelve los modelId disponibles para la cuenta,
// llamando a ListAvailableModels contra management.<region>.kiro.dev con el
// bearer token de la cuenta (con refresh vía auth.Manager). Cualquier fallo
// (auth, red, status≠200, parseo) se propaga como error para que el llamador
// caiga a la lista estática.
func (m *Manager) listModelsFromManagement(ctx context.Context, account *Account) ([]string, error) {
	token, err := account.Auth.AccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("management ListAvailableModels: access token: %w", err)
	}
	arn := account.Auth.ProfileARN()
	if arn == "" {
		arn = account.ProfileARN
	}
	region := account.Auth.Region()
	if region == "" {
		region = "us-east-1"
	}

	base := fmt.Sprintf("https://management.%s.kiro.dev/", region)
	if m.managementURLOverride != nil {
		base = m.managementURLOverride(region)
	}

	cctx, cancel := context.WithTimeout(ctx, managementModelsTimeout)
	defer cancel()
	client := &http.Client{Timeout: managementModelsTimeout}

	var models []string
	nextToken := ""
	for {
		ids, next, err := fetchManagementModelsPage(cctx, client, base, token, arn, nextToken)
		if err != nil {
			return nil, err
		}
		models = append(models, ids...)
		if next == "" {
			break
		}
		nextToken = next
	}
	return models, nil
}

// fetchManagementModelsPage hace UNA petición ListAvailableModels y devuelve los
// modelId de esa página más el nextToken (vacío si es la última).
func fetchManagementModelsPage(ctx context.Context, client *http.Client, base, token, arn, nextToken string) (ids []string, next string, err error) {
	q := url.Values{}
	q.Set("origin", "KIRO_CLI")
	if arn != "" {
		q.Set("profileArn", arn)
	}
	fullURL := base + "?" + q.Encode()

	payload := map[string]any{"origin": "KIRO_CLI"}
	if arn != "" {
		payload["profileArn"] = arn
	}
	if nextToken != "" {
		payload["nextToken"] = nextToken
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonCodeWhispererService.ListAvailableModels")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-amzn-codewhisperer-optout", "true")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("management ListAvailableModels: HTTP %d", resp.StatusCode)
	}

	var parsed struct {
		Models []struct {
			ModelID string `json:"modelId"`
		} `json:"models"`
		NextToken string `json:"nextToken"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, "", fmt.Errorf("management ListAvailableModels: parse: %w", err)
	}
	ids = make([]string, 0, len(parsed.Models))
	for _, mdl := range parsed.Models {
		if mdl.ModelID != "" {
			ids = append(ids, mdl.ModelID)
		}
	}
	return ids, parsed.NextToken, nil
}
