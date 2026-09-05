// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package kiroerrors

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// TestEnhanceAgainstCorpus valida Enhance contra los 21 casos golden grabados
// de kiro.kiro_errors:enhance_kiro_error.
//
// El original recibe un solo argumento posicional: un diccionario ya parseado
// desde el cuerpo JSON de la respuesta de Kiro. La firma en Go toma un
// map[string]json.RawMessage para preservar dos distinciones que el corpus
// exige y que una firma (message, reason string) no puede transportar:
//
//   - clave AUSENTE frente a clave PRESENTE con valor null
//   - clave PRESENTE con valor null frente a clave PRESENTE con cadena vacía
//
// La rama genérica del original mira "reason" in error_json literalmente
// (presencia de la clave), así que el test deserializa cada args[0] a
// map[string]json.RawMessage y llama a Enhance con eso.
func TestEnhanceAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "kiro_errors/enhance_kiro_error") {
		t.Run(c.Name, func(t *testing.T) {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &obj); err != nil {
				t.Fatalf("[%s] entrada: %v", c.Name, err)
			}
			var want Info
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("[%s] salida esperada: %v", c.Name, err)
			}
			if got := Enhance(obj); got != want {
				t.Errorf("[%s]\n  obtuve %+v\n  quiero %+v", c.Name, got, want)
			}
		})
	}
}
