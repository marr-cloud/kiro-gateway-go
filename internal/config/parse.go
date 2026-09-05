// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Reglas de parseo, todas verificadas fila a fila contra kiro/config.py:
//
//   - Booleano "normal": strings.ToLower(v) en {"true","1","yes"} activa;
//     cualquier otro valor (incluidos "" y valores no reconocidos) desactiva.
//     Fuente: os.getenv(X, "default").lower() in ("true", "1", "yes").
//
//   - Booleano invertido para FAKE_REASONING: strings.ToLower(v) en
//     {"false","0","no","disabled","off"} desactiva; cualquier otra cosa
//     (incluidos "" y "potato") activa. Fuente: `_FAKE_REASONING_RAW not in
//     ("false", "0", "no", "disabled", "off")`.
//
//   - Enum con degradación silenciosa: si strings.ToLower(v) no está en la
//     lista blanca, se devuelve el default sin error ni aviso. Fuente:
//     el bloque `if _RAW in (...): ... else: DEFAULT` de config.py para
//     DEBUG_MODE y FAKE_REASONING_HANDLING.
//
//   - Int y float: se hace strings.TrimSpace y strconv. Si el valor está
//     puesto pero es inválido, se devuelve error. Fuente: en config.py,
//     int(os.getenv(...)) y float(os.getenv(...)) sin manejo de error, así
//     que un valor inválido aborta la importación con ValueError. Se
//     replica devolviendo error desde Load; ver el paquete doc.

// parseString devuelve raw si isSet es true, o def en caso contrario. Un
// valor puesto pero vacío es un valor válido: gana al default, igual que
// hace os.getenv(X, def) cuando X está en el entorno con valor "".
func parseString(raw string, isSet bool, def string) string {
	if !isSet {
		return def
	}
	return raw
}

// parseBool implementa la regla general de booleanos ({"true","1","yes"}).
// Se toma el default como el bool que Python calcularía sobre la cadena
// pasada como default a os.getenv: para SQLITE_READONLY es "false" y por
// tanto false; para TRUNCATION_RECOVERY es "true" y por tanto true.
//
// Cuando isSet es true, def no se consulta: la comparación cae sobre raw
// tal cual, y un "" explícito da false porque "" no está en la lista de
// tokens de activación.
func parseBool(raw string, isSet bool, def bool) bool {
	if !isSet {
		return def
	}
	switch strings.ToLower(raw) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

// parseBoolInverted implementa la lógica invertida de FAKE_REASONING: sólo
// los tokens de la lista de desactivación devuelven false; cualquier otro
// valor, incluidos "" (unset) y "potato", devuelve true.
//
// No lleva flag isSet porque la fuente hace `os.getenv("FAKE_REASONING",
// "").lower() not in (...)`: unset y valor vacío se procesan idénticamente
// y ambos activan el modo por defecto.
func parseBoolInverted(raw string) bool {
	switch strings.ToLower(raw) {
	case "false", "0", "no", "disabled", "off":
		return false
	default:
		return true
	}
}

// parseEnum devuelve strings.ToLower(raw) si está en valid; en caso
// contrario, devuelve def sin error. Sirve para DEBUG_MODE y
// FAKE_REASONING_HANDLING. La lista valid debe estar ya en minúsculas.
func parseEnum(raw string, isSet bool, valid []string, def string) string {
	if !isSet {
		return def
	}
	lower := strings.ToLower(raw)
	for _, v := range valid {
		if lower == v {
			return lower
		}
	}
	return def
}

// parseInt trata "no puesto" como default sin error, y "puesto pero
// inválido" como error propagable a Load. TrimSpace refleja la tolerancia
// de int() de Python a espacios en los extremos.
func parseInt(raw string, isSet bool, def int) (int, error) {
	if !isSet {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("entero inválido %q: %w", raw, err)
	}
	return n, nil
}

// parseFloat es análoga a parseInt pero para float64.
func parseFloat(raw string, isSet bool, def float64) (float64, error) {
	if !isSet {
		return def, nil
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("float inválido %q: %w", raw, err)
	}
	return n, nil
}
