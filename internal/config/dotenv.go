// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package config

import (
	"bufio"
	"os"
	"strings"
)

// parseDotenvFile lee path y devuelve un mapa clave→valor con las líneas
// que consiga interpretar. El parser está deliberadamente alineado con
// _get_raw_env_value de kiro/config.py: NO procesa secuencias de escape,
// respeta el recorte de comillas simples y dobles cuando ambas coinciden,
// y salta líneas vacías, comentarios y líneas sin `=`.
//
// Todos los consumidores de .env dentro del paquete usan este mismo
// parser: no hay una variante "cruda" separada porque nunca interpretamos
// escapes en ningún camino. Esto simplifica la implementación y garantiza
// que las rutas Windows (KIRO_CREDS_FILE, KIRO_CLI_DB_FILE) sobreviven a
// pesar de que la lógica del original las trataba como caso especial.
//
// Fallos silenciosos por diseño: fichero inexistente, error de lectura o
// líneas malformadas se descartan sin ruido. Es lo que hace load_dotenv
// del original: si no hay .env, no hay problema.
//
// Cuando path es "", devuelve un mapa vacío sin tocar el disco. Es la
// forma en la que los tests de config declaran "no hay .env a considerar".
func parseDotenvFile(path string) map[string]string {
	out := map[string]string{}
	if path == "" {
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	// Ampliamos el buffer para tolerar líneas largas (rutas, tokens):
	// el default de bufio.Scanner es 64 KiB y una JSON URL con parámetros
	// se acerca al límite. 1 MiB es amplio y sigue barato.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		k, v, ok := parseDotenvLine(sc.Text())
		if !ok {
			continue
		}
		// La primera ocurrencia gana: es el orden natural y coincide con
		// el comportamiento por defecto de python-dotenv.
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return out
}

// parseDotenvLine emula el regex de config.py:
//
//	^KEY=(["']?)(.+?)\1\s*$
//
// tras aplicar strip() a la línea completa. Devuelve (key, value, true)
// cuando la línea contiene una asignación válida no vacía.
//
// La captura backreferenciada de la comilla ("igual apertura que cierre")
// se implementa manualmente porque el paquete `regexp` de Go usa RE2 y no
// soporta backreferences. La lógica resultante es una traducción literal
// del comportamiento del regex sobre los casos reales del .env.
func parseDotenvLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	eq := strings.IndexByte(line, '=')
	if eq <= 0 {
		return "", "", false
	}
	key := line[:eq]
	value := line[eq+1:]

	// Recorte de comillas: sólo si la primera y la última coinciden y son
	// del mismo tipo. Cadenas como `"unclosed` o `'mismatched"` se dejan
	// verbatim, que es lo que hace el regex del original tras backtrack.
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' || first == '\'') && first == last {
			return key, value[1 : len(value)-1], true
		}
	}
	if value == "" {
		// `.+?` requiere al menos un carácter; VAR= no es asignación.
		return "", "", false
	}
	return key, value, true
}
