// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package truncationstate

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestSetAndGetTool verifies basic SetTool/GetTool operations.
func TestSetAndGetTool(t *testing.T) {
	s := New()

	// Set a tool record
	record := map[string]any{"size_bytes": 1000, "reason": "test"}
	s.SetTool("call_123", record)

	// Get it back
	got, ok := s.GetTool("call_123")
	if !ok {
		t.Errorf("GetTool returned ok=false, want ok=true")
	}
	if got == nil {
		t.Errorf("GetTool returned nil, want record")
	}

	// Verify it was deleted
	got2, ok2 := s.GetTool("call_123")
	if ok2 {
		t.Errorf("GetTool after deletion returned ok=true, want ok=false")
	}
	if got2 != nil {
		t.Errorf("GetTool after deletion returned %v, want nil", got2)
	}
}

// TestGetNonexistentTool verifies GetTool returns false for missing keys.
func TestGetNonexistentTool(t *testing.T) {
	s := New()
	record, ok := s.GetTool("nonexistent")
	if ok {
		t.Errorf("GetTool on missing key returned ok=true, want ok=false")
	}
	if record != nil {
		t.Errorf("GetTool on missing key returned %v, want nil", record)
	}
}

// TestSetAndGetContent verifies basic SetContent/GetContent operations with SHA256 hashing.
func TestSetAndGetContent(t *testing.T) {
	s := New()

	content := "This is some truncated content that will be hashed"
	record := map[string]any{"message_hash": "abc123", "content_preview": content}
	s.SetContent(content, record)

	// Get it back using same content
	got, ok := s.GetContent(content)
	if !ok {
		t.Errorf("GetContent returned ok=false, want ok=true")
	}
	if got == nil {
		t.Errorf("GetContent returned nil, want record")
	}

	// Verify it was deleted
	got2, ok2 := s.GetContent(content)
	if ok2 {
		t.Errorf("GetContent after deletion returned ok=true, want ok=false")
	}
	if got2 != nil {
		t.Errorf("GetContent after deletion returned %v, want nil", got2)
	}
}

// TestGetNonexistentContent verifies GetContent returns false for missing keys.
func TestGetNonexistentContent(t *testing.T) {
	s := New()
	record, ok := s.GetContent("nonexistent content")
	if ok {
		t.Errorf("GetContent on missing key returned ok=true, want ok=false")
	}
	if record != nil {
		t.Errorf("GetContent on missing key returned %v, want nil", record)
	}
}

// TestContentHashingConsistency verifies that the same content always produces the same hash.
func TestContentHashingConsistency(t *testing.T) {
	s := New()

	content := "Same content for hashing test"
	record1 := map[string]any{"attempt": 1}
	record2 := map[string]any{"attempt": 2}

	// Set with content
	s.SetContent(content, record1)

	// Get it back using identical content
	got, ok := s.GetContent(content)
	if !ok || got == nil {
		t.Fatalf("First GetContent failed")
	}

	// Verify content hash is consistent by setting same hash with different content
	s.SetContent(content, record2)
	got2, ok2 := s.GetContent(content)
	if !ok2 || got2 == nil {
		t.Errorf("Second SetContent/GetContent failed")
	}
}

// TestContentHashingWith500CharBoundary verifies that only the first 500 chars are used in the hash.
func TestContentHashingWith500CharBoundary(t *testing.T) {
	s := New()

	// Create content longer than 500 chars
	content1 := "a"
	for i := 0; i < 100; i++ {
		content1 += "abcdefghij"
	} // 1001 chars
	content2 := content1[:500] + "DIFFERENT_TAIL"

	record := map[string]any{"test": "value"}
	s.SetContent(content1, record)

	// GetContent should find it using content2 (which differs only in tail)
	got, ok := s.GetContent(content2)
	if !ok || got == nil {
		t.Errorf("GetContent with different tail failed: hash should only use first 500 chars")
	}
}

// TestMultipleCachesIndependent verifies that tool cache and content cache are independent.
func TestMultipleCachesIndependent(t *testing.T) {
	s := New()

	toolRecord := map[string]any{"type": "tool"}
	contentRecord := map[string]any{"type": "content"}

	s.SetTool("call_1", toolRecord)
	s.SetContent("content", contentRecord)

	// Getting tool shouldn't affect content cache
	_, ok1 := s.GetTool("call_1")
	if !ok1 {
		t.Fatalf("GetTool failed")
	}

	_, ok2 := s.GetContent("content")
	if !ok2 {
		t.Errorf("GetContent failed after GetTool, caches should be independent")
	}
}

// TestConcurrencyStress runs 10 goroutines setting and getting tool records concurrently.
// This stress test verifies the mutex protection works correctly.
func TestConcurrencyStress(t *testing.T) {
	s := New()
	const numGoroutines = 10
	const operationsPerGoroutine = 100

	var wg sync.WaitGroup
	var successCount atomic.Int32

	// Goroutines will all try to set/get the same few keys
	toolIDs := []string{"tool_1", "tool_2", "tool_3", "tool_4", "tool_5"}

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for op := 0; op < operationsPerGoroutine; op++ {
				// Alternate between setting and getting
				toolIDIndex := (id + op) % len(toolIDs)
				toolID := toolIDs[toolIDIndex]

				if op%2 == 0 {
					// Set operation
					record := map[string]any{
						"goroutine": id,
						"operation": op,
						"data":      fmt.Sprintf("record_%d_%d", id, op),
					}
					s.SetTool(toolID, record)
				} else {
					// Get operation (may or may not find, depending on race)
					got, _ := s.GetTool(toolID)
					if got != nil {
						successCount.Add(1)
					}
				}
			}
		}(g)
	}

	wg.Wait()

	// Just verify some gets succeeded (some might not if they race with deletes)
	if successCount.Load() < 1 {
		t.Errorf("Very few or no successful gets in concurrent stress test")
	}
}

// TestConcurrencyStressContent runs 10 goroutines setting and getting content records concurrently.
func TestConcurrencyStressContent(t *testing.T) {
	s := New()
	const numGoroutines = 10

	var wg sync.WaitGroup

	// First phase: all goroutines set their content
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for op := 0; op < 10; op++ {
				content := fmt.Sprintf("content_%d_%d", id, op)
				record := map[string]any{
					"goroutine": id,
					"operation": op,
				}
				s.SetContent(content, record)
			}
		}(g)
	}
	wg.Wait()

	// Second phase: all goroutines get their content
	successCount := atomic.Int32{}
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for op := 0; op < 10; op++ {
				content := fmt.Sprintf("content_%d_%d", id, op)
				got, found := s.GetContent(content)
				if found && got != nil {
					successCount.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()

	// All sets followed by all gets should result in numGoroutines * 10 successes
	expected := int32(numGoroutines * 10)
	if successCount.Load() != expected {
		t.Errorf("Got %d successes, want %d", successCount.Load(), expected)
	}
}
