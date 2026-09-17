// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package truncationstate

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// State caches truncation information keyed by tool ID and content hash.
// All access is protected by mutex to ensure thread-safe reads and destructive writes.
type State struct {
	toolCache    map[string]any
	contentCache map[string]any
	mu           sync.Mutex
}

// New returns a new empty truncation state cache.
func New() *State {
	return &State{
		toolCache:    make(map[string]any),
		contentCache: make(map[string]any),
	}
}

// SetTool stores a truncation record for a tool call by its ID.
// Multiple calls to SetTool with the same toolID will overwrite the previous value.
func (s *State) SetTool(toolID string, record any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolCache[toolID] = record
}

// GetTool retrieves and removes a truncation record for a tool call.
// Returns the record and true if found, nil and false otherwise.
// This is a destructive operation: the record is removed from cache after retrieval.
func (s *State) GetTool(toolID string) (record any, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok = s.toolCache[toolID]
	if ok {
		delete(s.toolCache, toolID)
	}
	return record, ok
}

// SetContent stores a truncation record for content, keyed by SHA256 hash of the first 500 chars.
// The hash is computed internally from the content parameter.
func (s *State) SetContent(content string, record any) {
	hash := hashContentKey(content)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contentCache[hash] = record
}

// GetContent retrieves and removes a truncation record for content.
// The hash is computed internally from the content parameter.
// Returns the record and true if found, nil and false otherwise.
// This is a destructive operation: the record is removed from cache after retrieval.
func (s *State) GetContent(content string) (record any, ok bool) {
	hash := hashContentKey(content)
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok = s.contentCache[hash]
	if ok {
		delete(s.contentCache, hash)
	}
	return record, ok
}

// hashContentKey computes the SHA256 hash of the first 500 characters of content,
// returning the first 16 hex characters of the digest, matching the upstream behavior.
func hashContentKey(content string) string {
	// Use first 500 chars for hash (enough to be unique, not too much)
	if len(content) > 500 {
		content = content[:500]
	}
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])[:16]
}

// ComputeContentHash computes and returns the SHA256 hash of the first 500 characters
// of content, for logging or external reference. Exposed for Task 8b (SAVE side).
func ComputeContentHash(content string) string {
	return hashContentKey(content)
}
