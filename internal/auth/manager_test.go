// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

// writeCredsFile serializa obj como JSON en <dir>/creds.json y devuelve la
// ruta. t.TempDir() se encarga de la limpieza.
func writeCredsFile(t *testing.T, dir string, obj map[string]any) string {
	t.Helper()
	path := filepath.Join(dir, "creds.json")
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshaling creds fixture: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing creds fixture: %v", err)
	}
	return path
}

// relevantEnvVars son las variables de internal/config que pueden influir
// en la resolución de región o en la carga de credenciales. Los subtests
// de precedencia de región las fijan explícitamente con t.Setenv (que
// restaura el valor previo solo) para que el entorno real de quien corre
// el test no contamine las aserciones — el mismo problema que
// internal/config/config_test.go resuelve con su propio isolateEnv.
var relevantEnvVars = []string{
	"KIRO_API_REGION", "KIRO_REGION", "KIRO_CREDS_FILE",
	"REFRESH_TOKEN", "PROFILE_ARN",
}

func isolateRelevantEnv(t *testing.T) {
	t.Helper()
	for _, k := range relevantEnvVars {
		t.Setenv(k, "")
	}
}

// loadCfg aplica overrides sobre el entorno ya aislado y llama a
// config.Load, tal como haría el binario real.
func loadCfg(t *testing.T, overrides map[string]string) *config.Config {
	t.Helper()
	isolateRelevantEnv(t)
	for k, v := range overrides {
		t.Setenv(k, v)
	}
	cfg, err := config.Load(config.Options{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// --- Paso 1, bullet 1: carga desde JSON temporal -------------------------

func TestNewManager_LoadsKiroDesktopJSON(t *testing.T) {
	dir := t.TempDir()
	expiresAt := time.Date(2030, 1, 2, 3, 4, 5, 123456789, time.UTC)
	path := writeCredsFile(t, dir, map[string]any{
		"refreshToken": "rt-12345",
		"accessToken":  "at-67890",
		"profileArn":   "arn:aws:codewhisperer:eu-central-1:111122223333:profile/abc",
		"region":       "eu-central-1",
		"expiresAt":    expiresAt.Format(time.RFC3339Nano),
	})

	cfg := loadCfg(t, map[string]string{"KIRO_CREDS_FILE": path})

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if mgr.creds.RefreshToken != "rt-12345" {
		t.Errorf("RefreshToken = %q, want rt-12345", mgr.creds.RefreshToken)
	}
	if mgr.creds.AccessToken != "at-67890" {
		t.Errorf("AccessToken = %q, want at-67890", mgr.creds.AccessToken)
	}
	if mgr.ProfileARN() != "arn:aws:codewhisperer:eu-central-1:111122223333:profile/abc" {
		t.Errorf("ProfileARN() = %q", mgr.ProfileARN())
	}
	if mgr.creds.Region != "eu-central-1" {
		t.Errorf("creds.Region = %q, want eu-central-1", mgr.creds.Region)
	}
	if !mgr.tokens.ExpiresAt.Equal(expiresAt) {
		t.Errorf("tokens.ExpiresAt = %v, want %v", mgr.tokens.ExpiresAt, expiresAt)
	}
	if got, want := mgr.Type(), AuthTypeKiroDesktop; got != want {
		t.Errorf("Type() = %v, want %v", got, want)
	}
	if got, want := mgr.APIHost(), "https://runtime.eu-central-1.kiro.dev"; got != want {
		t.Errorf("APIHost() = %q, want %q", got, want)
	}
}

func TestNewManager_MissingCredsFileIsNonFatal(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.json")
	cfg := loadCfg(t, map[string]string{"KIRO_CREDS_FILE": missing})

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager should not fail on a missing creds file, got: %v", err)
	}
	if got := mgr.Type(); got != AuthTypeUnknown {
		t.Errorf("Type() = %v, want AuthTypeUnknown", got)
	}
}

// --- Paso 1, bullet 2: precedencia de región ------------------------------

func TestRegionPrecedence(t *testing.T) {
	t.Run("per-account override wins over everything else", func(t *testing.T) {
		dir := t.TempDir()
		path := writeCredsFile(t, dir, map[string]any{
			"refreshToken": "rt",
			"region":       "ap-southeast-2", // sería el nivel 3 si ganara
		})
		cfg := loadCfg(t, map[string]string{
			"KIRO_CREDS_FILE": path,
			"KIRO_API_REGION": "ca-central-1", // sería el nivel 2 si ganara
			"KIRO_REGION":     "sa-east-1",    // nivel 5
		})

		mgr, err := NewManagerForAccount(cfg, "ap-south-1") // nivel 1
		if err != nil {
			t.Fatalf("NewManagerForAccount: %v", err)
		}
		if got, want := mgr.APIHost(), "https://runtime.ap-south-1.kiro.dev"; got != want {
			t.Errorf("APIHost() = %q, want %q", got, want)
		}
	})

	t.Run("KIRO_API_REGION wins over detected and default", func(t *testing.T) {
		dir := t.TempDir()
		path := writeCredsFile(t, dir, map[string]any{
			"refreshToken": "rt",
			"region":       "ap-southeast-2", // sería el nivel 3 si ganara
		})
		cfg := loadCfg(t, map[string]string{
			"KIRO_CREDS_FILE": path,
			"KIRO_API_REGION": "ap-northeast-1",
			"KIRO_REGION":     "sa-east-1", // nivel 5
		})

		mgr, err := NewManager(cfg)
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		if got, want := mgr.APIHost(), "https://runtime.ap-northeast-1.kiro.dev"; got != want {
			t.Errorf("APIHost() = %q, want %q", got, want)
		}
	})

	t.Run("region derived from profileArn wins over KIRO_REGION default", func(t *testing.T) {
		dir := t.TempDir()
		// Sin 'region' en el JSON: fuerza a que la detección caiga al
		// ARN, replicando la lógica que el original solo tiene en el
		// loader de SQLite (auth.py:357-366) generalizada aquí — ver el
		// ruling de la Task 2.
		path := writeCredsFile(t, dir, map[string]any{
			"refreshToken": "rt",
			"profileArn":   "arn:aws:codewhisperer:eu-west-3:111122223333:profile/abc",
		})
		cfg := loadCfg(t, map[string]string{
			"KIRO_CREDS_FILE": path,
			"KIRO_REGION":     "sa-east-1", // nivel 5, no debería ganar
		})

		mgr, err := NewManager(cfg)
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		if got, want := mgr.APIHost(), "https://runtime.eu-west-3.kiro.dev"; got != want {
			t.Errorf("APIHost() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to KIRO_REGION when nothing else resolves", func(t *testing.T) {
		cfg := loadCfg(t, map[string]string{
			"KIRO_REGION": "sa-east-1",
		})

		mgr, err := NewManager(cfg)
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		if got, want := mgr.APIHost(), "https://runtime.sa-east-1.kiro.dev"; got != want {
			t.Errorf("APIHost() = %q, want %q", got, want)
		}
		if got, want := mgr.Region(), "sa-east-1"; got != want {
			t.Errorf("Region() = %q, want %q", got, want)
		}
	})
}

// TestResolveAPIRegion_SSORegionFallbackIsLive exercises resolveAPIRegion
// directly at level 4 (Credentials.SSORegion), which the JSON source alone
// can never reach observably through NewManager (it always sets Region and
// SSORegion to the same value — see json_source.go's Load). Fix round 1,
// Important #1: the reviewer found the pre-fix level 4 branch provably
// dead (it re-checked c.Region, which detectedRegion — level 3 — already
// consumed). This test proves the fixed level 4 (a genuinely independent
// Credentials.SSORegion field) is reachable, ahead of Task 4's SQLite
// source actually populating Region and SSORegion with different values.
func TestResolveAPIRegion_SSORegionFallbackIsLive(t *testing.T) {
	cfg := &config.Config{}                       // sin override KIRO_API_REGION
	creds := Credentials{SSORegion: "me-south-1"} // Region y ProfileARN vacíos: nivel 3 no resuelve nada
	got := resolveAPIRegion("", cfg, creds, "us-east-1")
	if want := "me-south-1"; got != want {
		t.Errorf("resolveAPIRegion() = %q, want %q (nivel 4, SSORegion)", got, want)
	}
}

// --- Paso 1, bullet 3: Fingerprint estable --------------------------------

func TestManager_FingerprintIsStable(t *testing.T) {
	cfg := loadCfg(t, nil)
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	first := mgr.Fingerprint()
	second := mgr.Fingerprint()
	if first == "" {
		t.Fatal("Fingerprint() returned empty string")
	}
	if first != second {
		t.Errorf("Fingerprint() not stable: %q != %q", first, second)
	}
}

// --- Paso 1, bullet 4: comportamiento de AccessToken ----------------------

func TestAccessToken_ReturnsCurrentTokenWhenNotExpiringSoon(t *testing.T) {
	dir := t.TempDir()
	future := time.Now().UTC().Add(2 * time.Hour) // muy por encima de tokenRefreshThreshold (600s)
	path := writeCredsFile(t, dir, map[string]any{
		"refreshToken": "rt",
		"accessToken":  "at-valid",
		"profileArn":   "arn:aws:codewhisperer:us-east-1:1:profile/x",
		"region":       "us-east-1",
		"expiresAt":    future.Format(time.RFC3339Nano),
	})
	cfg := loadCfg(t, map[string]string{"KIRO_CREDS_FILE": path})

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	token, err := mgr.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: unexpected error %v", err)
	}
	if token != "at-valid" {
		t.Errorf("AccessToken() = %q, want at-valid", token)
	}
}

// TestAccessToken_ExpiringSoonSurfacesRefreshError cubre el ruling de la
// Task 2: cuando el token está por expirar, AccessToken intenta refrescar
// y, como el refresco todavía es un stub, el error se PROPAGA en vez de
// degradar con gracia. La degradación grácil del original (auth.py:906-919)
// es exclusiva del modo SQLite ante un 400 de OIDC; en modo JSON/Kiro
// Desktop el fallo de refresco siempre se propaga (auth.py:925-929), que es
// exactamente el modo que esta Task implementa.
func TestAccessToken_ExpiringSoonSurfacesRefreshError(t *testing.T) {
	dir := t.TempDir()
	soon := time.Now().UTC().Add(100 * time.Second) // dentro del umbral de 600s
	path := writeCredsFile(t, dir, map[string]any{
		"refreshToken": "rt",
		"accessToken":  "at-stale",
		"profileArn":   "arn:aws:codewhisperer:us-east-1:1:profile/x",
		"region":       "us-east-1",
		"expiresAt":    soon.Format(time.RFC3339Nano),
	})
	cfg := loadCfg(t, map[string]string{"KIRO_CREDS_FILE": path})

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.AccessToken(context.Background())
	if err == nil {
		t.Fatal("AccessToken: expected an error while the refresh path is a stub, got nil")
	}
	if !errors.Is(err, ErrRefreshNotImplemented) {
		t.Errorf("AccessToken error = %v, want errors.Is(ErrRefreshNotImplemented)", err)
	}
}

func TestForceRefresh_StubIsReachableAndStable(t *testing.T) {
	cfg := loadCfg(t, nil)
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err1 := mgr.ForceRefresh(context.Background())
	_, err2 := mgr.ForceRefresh(context.Background())
	if !errors.Is(err1, ErrRefreshNotImplemented) || !errors.Is(err2, ErrRefreshNotImplemented) {
		t.Errorf("ForceRefresh errors = (%v, %v), want both ErrRefreshNotImplemented", err1, err2)
	}
}
