// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package auth

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// OIDCError wraps an OIDC refresh error with the HTTP status code, allowing
// graceful degradation logic to distinguish 400 errors (invalid_request, stale
// token) from other failures. This type is internal to the refresh flow.
type OIDCError struct {
	StatusCode int
	Err        error
}

func (e *OIDCError) Error() string {
	return e.Err.Error()
}

func (e *OIDCError) Unwrap() error {
	return e.Err
}

// Refresh triggers a refresh via singleflight coalescing. Called by AccessToken
// when the token is expiring soon, and by ForceRefresh unconditionally.
// Returns the fresh access token or, in the graceful-degradation case, the old
// still-valid token when the refresh call failed but the token isn't truly
// expired yet.
//
// Graceful degradation (returning the old token on failure) applies only to
// SQLite-sourced (AuthTypeKiroCLI) credentials when the OIDC refresh fails
// with HTTP 400, and only if the pre-refresh token is not yet expired
// (auth.py:906-919). For all other cases (JSON source, non-400 errors, or
// already-expired tokens), the error is propagated.
func (m *Manager) Refresh(ctx context.Context) (string, error) {
	// Check token outside lock first: if not expiring soon, no need to refresh.
	// This is an optimization to avoid the singleflight call for fresh tokens.
	m.mu.Lock()
	now := time.Now().UTC()
	if !m.tokens.IsExpiringSoon(now, tokenRefreshThreshold) {
		token := m.tokens.AccessToken
		m.mu.Unlock()
		if token != "" {
			return token, nil
		}
	} else {
		m.mu.Unlock()
	}

	// Token is expiring soon; use singleflight to coalesce concurrent refresh attempts.
	return m.refreshCoalesced(ctx, false)
}

// refreshCoalesced uses singleflight to coalesce concurrent refresh attempts.
// Refresh() calls and ForceRefresh() calls use separate singleflight keys to
// prevent one from blocking the other:
//   - Multiple Refresh() calls (force=false) coalesce together with key "refresh"
//   - Multiple ForceRefresh() calls (force=true) coalesce together with key "refresh-force"
//   - Refresh() and ForceRefresh() never interfere with each other
func (m *Manager) refreshCoalesced(ctx context.Context, force bool) (string, error) {
	// Use different singleflight key for force vs normal refresh
	key := "refresh"
	if force {
		key = "refresh-force"
	}

	v, err, _ := m.sfGroup.Do(key, func() (interface{}, error) {
		return m.doRefresh(ctx, force)
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

// doRefresh performs the actual refresh work under lock. It is called inside
// singleflight, so multiple concurrent callers are coalesced into one call.
// The force parameter is passed directly from refreshCoalesced via the closure.
func (m *Manager) doRefresh(ctx context.Context, force bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()

	// Double-check under lock: if not forcing and token is now fresh (someone else
	// may have refreshed while this call was waiting in singleflight queue),
	// return current token without attempting refresh.
	if !force && !m.tokens.IsExpiringSoon(now, tokenRefreshThreshold) {
		if m.tokens.AccessToken != "" {
			return m.tokens.AccessToken, nil
		}
	}

	// Save old token for graceful degradation fallback
	oldToken := m.tokens.AccessToken
	oldExpires := m.tokens.ExpiresAt

	// Attempt refresh
	_, err := m.refreshLocked(ctx)

	// If success, return new token
	if err == nil {
		return m.tokens.AccessToken, nil
	}

	// If failure and not forced, apply graceful degradation for SQLite+400 case
	// per upstream auth.py:906-919: SQLite-sourced credentials that fail with 400
	// may be used again if the token isn't truly expired yet.
	if !force {
		var oidcErr *OIDCError
		if errors.As(err, &oidcErr) && oidcErr.StatusCode == http.StatusBadRequest && m.authType == AuthTypeKiroCLI {
			// Graceful degradation: check if old token is still valid
			if oldToken != "" && !m.tokens.IsExpired(now) {
				// Return the pre-refresh token since it's still valid
				m.tokens.AccessToken = oldToken
				m.tokens.ExpiresAt = oldExpires
				return oldToken, nil
			}
		}
	}

	// ForceRefresh never applies graceful degradation, and all other cases propagate the error
	return "", err
}
