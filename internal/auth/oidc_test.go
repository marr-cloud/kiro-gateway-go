// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeSource es un Source de test in-memory: registra cada Credentials que
// Save recibe, para que los tests puedan verificar el write-through sin
// tocar disco.
type fakeSource struct {
	saved []Credentials
}

func (f *fakeSource) Load() (Credentials, error) { return Credentials{}, nil }
func (f *fakeSource) Save(c Credentials) error {
	f.saved = append(f.saved, c)
	return nil
}

// injectHomeDirForTest inyecta un homeDir personalizado en mgr para el test.
// Fix round 1 (Critical 1): trasladado de withHomeDirOverride (que modificaba
// el var de paquete homeDirFunc) a un parámetro del struct Manager.
func injectHomeDirForTest(mgr *Manager, dir string) {
	mgr.homeDir = func() (string, error) { return dir, nil }
}

// --- Success --------------------------------------------------------------

func TestRefreshAWSSSO_Success(t *testing.T) {
	var gotBody map[string]any
	var gotContentType string
	var requests int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accessToken":"at-new","refreshToken":"rt-new","expiresIn":3600}`))
	}))
	defer srv.Close()

	mgr, src := managerForOIDCTest(t, srv, AuthTypeAWSSSO)

	if err := mgr.refreshAWSSSO(context.Background()); err != nil {
		t.Fatalf("refreshAWSSSO: %v", err)
	}

	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}

	// Body shape: camelCase, cuatro campos exactos.
	want := map[string]any{
		"grantType":    "refresh_token",
		"clientId":     "client-id",
		"clientSecret": "client-secret",
		"refreshToken": "rt-old",
	}
	for k, v := range want {
		if gotBody[k] != v {
			t.Errorf("request body[%q] = %v, want %v", k, gotBody[k], v)
		}
	}
	if len(gotBody) != len(want) {
		t.Errorf("request body has %d fields (%v), want exactly %d", len(gotBody), gotBody, len(want))
	}

	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}

	if mgr.tokens.AccessToken != "at-new" {
		t.Errorf("tokens.AccessToken = %q, want at-new", mgr.tokens.AccessToken)
	}
	if mgr.refreshToken != "rt-new" {
		t.Errorf("refreshToken = %q, want rt-new (rotated)", mgr.refreshToken)
	}

	wantExpiresAt := time.Now().UTC().Add((3600 - 60) * time.Second).Truncate(time.Second)
	gotExpiresAt := mgr.tokens.ExpiresAt.Truncate(time.Second)
	diff := gotExpiresAt.Sub(wantExpiresAt)
	if diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("tokens.ExpiresAt = %v, want ~%v (expiresIn=3600 minus 60s buffer)", gotExpiresAt, wantExpiresAt)
	}

	if len(src.saved) != 1 {
		t.Fatalf("Save calls = %d, want 1", len(src.saved))
	}
	if src.saved[0].AccessToken != "at-new" {
		t.Errorf("saved AccessToken = %q, want at-new", src.saved[0].AccessToken)
	}
}

// --- Enterprise device registration ---------------------------------------

func TestRefreshAWSSSO_Enterprise(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".aws", "sso", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	regFile := filepath.Join(cacheDir, "enterprise-hash.json")
	if err := os.WriteFile(regFile, []byte(`{"clientId":"ent-client-id","clientSecret":"ent-client-secret"}`), 0o600); err != nil {
		t.Fatalf("writing device registration fixture: %v", err)
	}

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decoding request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accessToken":"at-ent","expiresIn":3600}`))
	}))
	defer srv.Close()

	mgr := &Manager{
		authType: AuthTypeAWSSSO,
		region:   "us-east-1",
		creds: Credentials{
			ClientIDHash: "enterprise-hash",
			SSORegion:    "us-east-1",
		},
		refreshToken: "rt-ent",
	}
	// Fix round 1 (Critical 1): inyectar homeDir y oidcURL directamente en el Manager
	injectHomeDirForTest(mgr, dir)
	mgr.oidcURL = func(string) string { return srv.URL }

	if err := mgr.refreshAWSSSO(context.Background()); err != nil {
		t.Fatalf("refreshAWSSSO: %v", err)
	}

	if gotBody["clientId"] != "ent-client-id" {
		t.Errorf("request clientId = %v, want ent-client-id (should come from device registration file)", gotBody["clientId"])
	}
	if gotBody["clientSecret"] != "ent-client-secret" {
		t.Errorf("request clientSecret = %v, want ent-client-secret", gotBody["clientSecret"])
	}
	if mgr.creds.ClientID != "ent-client-id" || mgr.creds.ClientSecret != "ent-client-secret" {
		t.Errorf("creds.ClientID/ClientSecret not resolved: %+v", mgr.creds)
	}
	if mgr.tokens.AccessToken != "at-ent" {
		t.Errorf("tokens.AccessToken = %q, want at-ent", mgr.tokens.AccessToken)
	}
}

func TestLoadEnterpriseDeviceRegistration_FileMissing(t *testing.T) {
	dir := t.TempDir()

	mgr := &Manager{creds: Credentials{ClientIDHash: "missing-hash"}}
	injectHomeDirForTest(mgr, dir)
	if err := mgr.loadEnterpriseDeviceRegistration("missing-hash"); err != nil {
		t.Fatalf("loadEnterpriseDeviceRegistration: want nil error for missing file, got %v", err)
	}
	if mgr.creds.ClientID != "" || mgr.creds.ClientSecret != "" {
		t.Errorf("creds mutated despite missing file: %+v", mgr.creds)
	}
}

func TestLoadEnterpriseDeviceRegistration_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, ".aws", "sso", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "bad-hash.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("writing malformed fixture: %v", err)
	}

	mgr := &Manager{creds: Credentials{ClientIDHash: "bad-hash"}}
	injectHomeDirForTest(mgr, dir)
	if err := mgr.loadEnterpriseDeviceRegistration("bad-hash"); err != nil {
		t.Fatalf("loadEnterpriseDeviceRegistration: want nil error for malformed JSON, got %v", err)
	}
	if mgr.creds.ClientID != "" || mgr.creds.ClientSecret != "" {
		t.Errorf("creds mutated despite malformed file: %+v", mgr.creds)
	}
}

// --- 400 invalid_client, non-KiroCLI: single attempt, error propagates ----

func TestRefreshAWSSSO_400_NonKiroCLI_NoRetry(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer srv.Close()

	mgr, _ := managerForOIDCTest(t, srv, AuthTypeAWSSSO) // NOT AuthTypeKiroCLI

	before := mgr.tokens

	err := mgr.refreshAWSSSO(context.Background())
	if err == nil {
		t.Fatal("refreshAWSSSO: want error on 400, got nil")
	}
	if requests != 1 {
		t.Errorf("requests = %d, want exactly 1 (no retry for non-KiroCLI authType)", requests)
	}
	if mgr.tokens != before {
		t.Errorf("tokens mutated on failed refresh: got %+v, want unchanged %+v", mgr.tokens, before)
	}
}

// --- 400, KiroCLI: reload-from-sqlite stub fires, retries once, succeeds --

func TestRefreshAWSSSO_400_KiroCLI_RetriesOnceAndSucceeds(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accessToken":"at-after-retry","expiresIn":3600}`))
	}))
	defer srv.Close()

	mgr, _ := managerForOIDCTest(t, srv, AuthTypeKiroCLI)

	if err := mgr.refreshAWSSSO(context.Background()); err != nil {
		t.Fatalf("refreshAWSSSO: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want exactly 2 (one failed attempt + one retry)", requests)
	}
	if mgr.tokens.AccessToken != "at-after-retry" {
		t.Errorf("tokens.AccessToken = %q, want at-after-retry", mgr.tokens.AccessToken)
	}
}

// --- Non-400 error (500): error propagates, no retry, tokens unchanged ---

func TestRefreshAWSSSO_500_NoRetry(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal error`))
	}))
	defer srv.Close()

	// Even AuthTypeKiroCLI must not retry on a non-400 status.
	mgr, _ := managerForOIDCTest(t, srv, AuthTypeKiroCLI)
	before := mgr.tokens

	err := mgr.refreshAWSSSO(context.Background())
	if err == nil {
		t.Fatal("refreshAWSSSO: want error on 500, got nil")
	}
	if requests != 1 {
		t.Errorf("requests = %d, want exactly 1 (no retry for non-400 status)", requests)
	}
	if mgr.tokens != before {
		t.Errorf("tokens mutated on failed refresh: got %+v, want unchanged %+v", mgr.tokens, before)
	}
}

// --- helpers ---------------------------------------------------------------

// managerForOIDCTest construye un Manager listo para ejercitar
// refreshAWSSSO contra srv, con credenciales AWS SSO OIDC "normales" (no
// Enterprise) ya pobladas. Fix round 1 (Critical 1): inyecta oidcURL
// directamente en el Manager en lugar de modificar un var de paquete.
func managerForOIDCTest(t *testing.T, srv *httptest.Server, authType AuthType) (*Manager, *fakeSource) {
	t.Helper()
	src := &fakeSource{}
	mgr := &Manager{
		authType: authType,
		region:   "us-east-1",
		source:   src,
		creds: Credentials{
			ClientID:     "client-id",
			ClientSecret: "client-secret",
			SSORegion:    "us-east-1",
		},
		refreshToken: "rt-old",
		// Fix round 1 (Critical 1): inyectar oidcURL para que apunte al httptest.Server
		oidcURL: func(string) string { return srv.URL },
	}
	return mgr, src
}
