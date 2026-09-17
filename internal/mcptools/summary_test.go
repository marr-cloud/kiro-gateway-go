// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package mcptools

import (
	"strings"
	"testing"
)

func TestGenerateSearchSummary_WrapsInTagsAndFormatsResult(t *testing.T) {
	results := map[string]any{
		"results": []any{
			map[string]any{
				"title":         "Learn Python",
				"url":           "https://python.org/tutorial",
				"snippet":       "Official Python tutorial",
				"publishedDate": float64(1710339825000), // 2024-03-13T...
			},
		},
		"totalResults": float64(1),
	}

	got := GenerateSearchSummary("python tutorials", results)

	if !strings.HasPrefix(got, "\n<web_search>\nSearch results for \"python tutorials\":\n\n") {
		t.Errorf("prefijo inesperado: %q", got)
	}
	if !strings.HasSuffix(got, "</web_search>\n") {
		t.Errorf("sufijo inesperado: %q", got)
	}
	if !strings.Contains(got, "1. Title: **Learn Python**\n") {
		t.Errorf("falta la línea de título: %q", got)
	}
	if !strings.Contains(got, "URL: https://python.org/tutorial\n") {
		t.Errorf("falta la línea de URL: %q", got)
	}
	if !strings.Contains(got, "Official Python tutorial\n") {
		t.Errorf("falta el snippet completo (sin truncar): %q", got)
	}
	if !strings.Contains(got, "Published: ") {
		t.Errorf("falta la línea Published: %q", got)
	}
}

func TestGenerateSearchSummary_NoResultsKey(t *testing.T) {
	got := GenerateSearchSummary("q", map[string]any{})
	if !strings.Contains(got, "No results found.\n") {
		t.Errorf("want %q en la salida, got %q", "No results found.", got)
	}
}

func TestGenerateSearchSummary_NilResults(t *testing.T) {
	got := GenerateSearchSummary("q", nil)
	if !strings.Contains(got, "No results found.\n") {
		t.Errorf("want %q en la salida, got %q", "No results found.", got)
	}
}

// TestGenerateSearchSummary_EmptyResultsListNoFallbackMessage replica la
// rareza de mcp_tools.py:237-269: si "results" en el dict está presente
// (aunque sea una lista vacía) y el dict no está vacío, el bucle no produce
// nada pero el mensaje "No results found." NO se añade (esa rama solo se
// toma si falta la clave o el dict es falsy).
func TestGenerateSearchSummary_EmptyResultsListNoFallbackMessage(t *testing.T) {
	got := GenerateSearchSummary("q", map[string]any{"results": []any{}, "totalResults": float64(0)})
	if strings.Contains(got, "No results found.") {
		t.Errorf("no debería aparecer 'No results found.' cuando la clave results está presente: %q", got)
	}
	if !strings.HasSuffix(got, "</web_search>\n") {
		t.Errorf("sufijo inesperado: %q", got)
	}
}

func TestGenerateSearchSummary_MissingTitleDefaultsToUntitled(t *testing.T) {
	results := map[string]any{
		"results": []any{map[string]any{"url": "https://x", "snippet": "s"}},
	}
	got := GenerateSearchSummary("q", results)
	if !strings.Contains(got, "Title: **Untitled**") {
		t.Errorf("want default Untitled, got %q", got)
	}
}

// TestChunkRunes_100CharChunks cubre el escenario del brief ("GenerateSearchSummary
// trocea en 100 chars"): la lógica de troceo real vive en sse.go (compartida
// por los dos generadores SSE, mcp_tools.py:399-406,491-505), este test
// verifica el helper directamente sobre el texto que GenerateSearchSummary
// produce.
func TestChunkRunes_100CharChunks(t *testing.T) {
	results := map[string]any{
		"results": []any{
			map[string]any{"title": "T", "url": "https://x", "snippet": strings.Repeat("a", 250)},
		},
	}
	summary := GenerateSearchSummary("q", results)
	if len(summary) <= 100 {
		t.Fatalf("el fixture de test debería producir un summary > 100 chars, got %d", len(summary))
	}

	chunks := chunkRunes(summary, 100)

	var rejoined strings.Builder
	for i, c := range chunks {
		if i < len(chunks)-1 && len([]rune(c)) != 100 {
			t.Errorf("chunk %d tiene %d runas, want 100 (salvo el último)", i, len([]rune(c)))
		}
		if len([]rune(c)) > 100 {
			t.Errorf("chunk %d excede 100 runas: %d", i, len([]rune(c)))
		}
		rejoined.WriteString(c)
	}
	if rejoined.String() != summary {
		t.Errorf("los chunks concatenados no reconstruyen el summary original")
	}
}

// TestChunkRunes_UnicodeSafe confirma que el troceo opera sobre code points,
// no bytes (igual que el slicing de Python 3 sobre str) — relevante porque
// snippets/títulos de resultados de búsqueda pueden traer UTF-8 multibyte.
func TestChunkRunes_UnicodeSafe(t *testing.T) {
	s := strings.Repeat("á", 150) // cada 'á' es 2 bytes en UTF-8, 1 rune
	chunks := chunkRunes(s, 100)
	if len(chunks) != 2 {
		t.Fatalf("want 2 chunks, got %d", len(chunks))
	}
	if len([]rune(chunks[0])) != 100 || len([]rune(chunks[1])) != 50 {
		t.Errorf("chunks tienen tamaños inesperados: %d, %d", len([]rune(chunks[0])), len([]rune(chunks[1])))
	}
}
