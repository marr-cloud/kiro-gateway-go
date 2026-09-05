// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accounterrors

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// TestClassifyAgainstCorpus valida Classify contra los 27 casos golden
// grabados de kiro.account_errors:classify_error.
//
// La firma real del original es classify_error(status_code: int,
// reason: Optional[str]): no tiene valores por defecto. Los casos del corpus
// con "args": [] pasan los valores por KWARGS (status_code, reason), no por
// defaults; los demás casos pasan posicional. Además reason puede ser JSON
// null (Optional[str] con None), que se representa aquí como cadena vacía.
func TestClassifyAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "account_errors/classify_error") {
		t.Run(c.Name, func(t *testing.T) {
			var status int
			var reason string // "" cuando el corpus trae null o no hay reason

			args := testutil.Args(t, c.Input)
			switch {
			case len(args) >= 2:
				if err := json.Unmarshal(args[0], &status); err != nil {
					t.Fatalf("[%s] arg 0: %v", c.Name, err)
				}
				// args[1] puede ser null -> reason queda ""
				if string(args[1]) != "null" {
					if err := json.Unmarshal(args[1], &reason); err != nil {
						t.Fatalf("[%s] arg 1: %v", c.Name, err)
					}
				}
			default: // 0 argumentos posicionales: los valores vienen por kwargs
				kw := testutil.Kwargs(t, c.Input)
				if err := json.Unmarshal(kw["status_code"], &status); err != nil {
					t.Fatalf("[%s] kwarg status_code: %v", c.Name, err)
				}
				if raw, ok := kw["reason"]; ok && string(raw) != "null" {
					if err := json.Unmarshal(raw, &reason); err != nil {
						t.Fatalf("[%s] kwarg reason: %v", c.Name, err)
					}
				}
			}

			var want string
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("[%s] salida esperada: %v", c.Name, err)
			}
			if got := string(Classify(status, reason)); got != want {
				t.Errorf("[%s] Classify(%d, %q) = %q, quiero %q", c.Name, status, reason, got, want)
			}
		})
	}
}

// TestInvalidModelIDIsRecoverable fija la rama 400 + INVALID_MODEL_ID -> Recoverable
// exigida por la fuente (account_errors.py). Es la única sub-rama del código 400
// que no coincide con el default Fatal, y a la vez la que gobierna el failover a
// otra cuenta cuando el modelo no está disponible en la suscripción actual.
// Los 27 casos golden de classify_error no cubren esta combinación concreta, así
// que si alguien invirtiese el orden del switch por descuido la paridad seguiría
// verde. Este test hecho a mano cierra ese hueco.
func TestInvalidModelIDIsRecoverable(t *testing.T) {
	if got := Classify(400, ReasonInvalidModelID); got != Recoverable {
		t.Errorf("Classify(400, INVALID_MODEL_ID) = %q, quiero recoverable", got)
	}
}
