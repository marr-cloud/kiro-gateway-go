// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package mcptools

import (
	"strconv"
	"strings"
	"time"
)

// searchResultItem es un elemento de results["results"] ya normalizado a
// tipos Go. Se comparte entre GenerateSearchSummary (este archivo) y los
// constructores de "web_search_tool_result" de sse.go/websearch.go — los
// tres leen el mismo shape de resultado MCP (mcp_tools.py:239-242,366-373,
// 714-721).
//
// Los campos string quedan en "" tanto si la clave falta como si vino
// presente-pero-vacía: el original distingue esos dos casos con
// dict.get(key, default) (p.ej. title cae a "Untitled" solo si la clave
// FALTA, no si vale ""), una distinción que se pierde al pasar por
// map[string]any + zero values de Go. Es una simplificación deliberada:
// ningún resultado real de búsqueda trae un título vacío-pero-presente, y
// ningún escenario del brief la ejercita.
type searchResultItem struct {
	Title            string
	URL              string
	Snippet          string
	PublishedDateMs  float64
	HasPublishedDate bool // replica `if published_date_ms:` (falsy en 0/None/ausente)
}

// hasResultsKey replica `results and "results" in results` (mcp_tools.py:237):
// el dict debe ser no vacío Y contener la clave "results", sin importar qué
// valor tenga esa clave.
func hasResultsKey(results map[string]any) bool {
	if len(results) == 0 {
		return false
	}
	_, ok := results["results"]
	return ok
}

// extractSearchResults lee results["results"] de forma defensiva: si la
// clave falta o no es una lista, o un elemento no es un objeto, se ignora
// (el original dejaría propagar un TypeError en ese caso — un JSON de Kiro
// malformado hasta ese punto no está entre los escenarios del brief, y
// fallar cerrado devolviendo menos resultados es preferible a un panic).
func extractSearchResults(results map[string]any) []searchResultItem {
	raw, _ := results["results"].([]any)
	items := make([]searchResultItem, 0, len(raw))
	for _, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		item := searchResultItem{
			Title:   getString(m, "title"),
			URL:     getString(m, "url"),
			Snippet: getString(m, "snippet"),
		}
		if v, ok := m["publishedDate"]; ok {
			if f, isNum := v.(float64); isNum && f != 0 {
				item.PublishedDateMs = f
				item.HasPublishedDate = true
			}
		}
		items = append(items, item)
	}
	return items
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// GenerateSearchSummary construye el resumen legible envuelto en
// <web_search>...</web_search> que ve el modelo. Puerto de
// mcp_tools.py:205-274 (generate_search_summary). Devuelve el snippet
// COMPLETO, sin truncar — el modelo necesita la información entera; el
// troceo en fragmentos de 100 caracteres para las deltas SSE es una
// preocupación de transporte separada (sse.go::chunkRunes), no de este
// resumen.
func GenerateSearchSummary(query string, results map[string]any) string {
	var b strings.Builder
	b.WriteString("\n<web_search>\nSearch results for \"")
	b.WriteString(query)
	b.WriteString("\":\n\n")

	if hasResultsKey(results) {
		for i, item := range extractSearchResults(results) {
			title := item.Title
			if title == "" {
				title = "Untitled"
			}
			b.WriteString(strconv.Itoa(i + 1))
			b.WriteString(". Title: **")
			b.WriteString(title)
			b.WriteString("**\n")

			if item.HasPublishedDate {
				dt := time.UnixMilli(int64(item.PublishedDateMs))
				b.WriteString("   Published: ")
				b.WriteString(dt.Format("02 Jan 2006 15:04:05"))
				b.WriteString("\n")
			}

			if item.URL != "" {
				b.WriteString("   URL: ")
				b.WriteString(item.URL)
				b.WriteString("\n")
			}

			if item.Snippet != "" {
				b.WriteString("   ")
				b.WriteString(item.Snippet)
				b.WriteString("\n")
			}

			b.WriteString("\n")
		}
	} else {
		b.WriteString("No results found.\n")
	}

	b.WriteString("</web_search>\n")
	return b.String()
}
