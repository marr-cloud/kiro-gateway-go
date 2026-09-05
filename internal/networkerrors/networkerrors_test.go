// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package networkerrors

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"os"
	"reflect"
	"syscall"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// TestClassifyAgainstCorpus verifica la lógica de decisión contra los 27 casos
// grabados de kiro.network_errors:classify_network_error.
//
// Los casos del corpus son excepciones de Python (marcador __exception__). En Go
// esos tipos no existen: se traducen a un errDesc por (module, type) usando la
// tabla exceptionKindTable de abajo. Esa tabla no es una equivalencia de
// producción, es el puente por el que los casos golden llegan a classifyDesc,
// que es el que la fase 4 va a llamar desde código de producción a través de
// Classify. La correspondencia real Go → clase de fallo se verifica en
// TestClassifyGoErrorTranslation.
func TestClassifyAgainstCorpus(t *testing.T) {
	cases := testutil.LoadCorpus(t, "network_errors/classify_network_error")
	if len(cases) != 27 {
		t.Fatalf("se esperaban 27 casos de classify_network_error, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			raw := testutil.Arg(t, c.Input, 0)
			if !testutil.IsException(raw) {
				t.Fatalf("[%s] args[0] no es __exception__: %s", c.Name, raw)
			}
			exc, err := testutil.DecodeException(raw)
			if err != nil {
				t.Fatalf("[%s] decodificando excepción: %v", c.Name, err)
			}
			desc := descFromException(t, c.Name, exc)

			got := classifyDesc(desc)

			var want Info
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("[%s] salida esperada: %v", c.Name, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("[%s] typeName=%q\n  obtuve  %+v\n  quiero  %+v",
					c.Name, exc.Type, got, want)
			}
		})
	}
}

// TestFormatAgainstCorpus verifica FormatForUser contra los 4 casos grabados de
// kiro.network_errors:format_error_for_user.
//
// La comparación es orden-insensible: se serializa got a JSON y ambos se
// deserializan a `any`, para no depender del orden de claves de json.Marshal.
func TestFormatAgainstCorpus(t *testing.T) {
	cases := testutil.LoadCorpus(t, "network_errors/format_error_for_user")
	if len(cases) != 4 {
		t.Fatalf("se esperaban 4 casos de format_error_for_user, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var info Info
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &info); err != nil {
				t.Fatalf("[%s] deserializando Info: %v", c.Name, err)
			}
			kw := testutil.Kwargs(t, c.Input)

			var formatType string
			if raw, ok := kw["format_type"]; ok {
				if err := json.Unmarshal(raw, &formatType); err != nil {
					t.Fatalf("[%s] format_type: %v", c.Name, err)
				}
			} else {
				formatType = "openai"
			}

			// El original tiene include_troubleshooting=True por defecto.
			includeTS := true
			if raw, ok := kw["include_troubleshooting"]; ok {
				if err := json.Unmarshal(raw, &includeTS); err != nil {
					t.Fatalf("[%s] include_troubleshooting: %v", c.Name, err)
				}
			}

			got := FormatForUser(info, formatType, includeTS)

			gotJSON, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("[%s] serializando got: %v", c.Name, err)
			}
			var gotAny, wantAny any
			if err := json.Unmarshal(gotJSON, &gotAny); err != nil {
				t.Fatalf("[%s] normalizando got: %v", c.Name, err)
			}
			if err := json.Unmarshal(c.Output, &wantAny); err != nil {
				t.Fatalf("[%s] normalizando want: %v", c.Name, err)
			}
			if !reflect.DeepEqual(gotAny, wantAny) {
				t.Errorf("[%s] format_type=%q include_troubleshooting=%v\n  obtuve  %s\n  quiero  %s",
					c.Name, formatType, includeTS, gotJSON, c.Output)
			}
		})
	}
}

// TestShortAgainstCorpus verifica ShortMessage contra los 9 casos grabados de
// kiro.network_errors:get_short_error_message.
func TestShortAgainstCorpus(t *testing.T) {
	cases := testutil.LoadCorpus(t, "network_errors/get_short_error_message")
	if len(cases) != 9 {
		t.Fatalf("se esperaban 9 casos de get_short_error_message, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var info Info
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &info); err != nil {
				t.Fatalf("[%s] deserializando Info: %v", c.Name, err)
			}
			var want string
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("[%s] salida esperada: %v", c.Name, err)
			}
			if got := ShortMessage(info); got != want {
				t.Errorf("[%s] ShortMessage = %q, quiero %q", c.Name, got, want)
			}
		})
	}
}

// TestClassifyGoErrorTranslation verifica la traducción de errores de Go a las
// categorías del clasificador. Es la parte que el corpus NO puede cubrir: el
// original recibe excepciones de httpx y en Go llegarán errores de net/http,
// net, crypto/tls, os y context. La tabla de correspondencia se documenta en
// classify.go y se comprueba aquí sintetizando cada error en memoria, sin abrir
// sockets. Se comprueban las tres propiedades observables que interesan a la
// fase 4: Category, IsRetryable y SuggestedHTTPCode. TechnicalDetails no se
// verifica porque su formato depende del texto de error del runtime y no es
// parte del contrato.
func TestClassifyGoErrorTranslation(t *testing.T) {
	// Simula un net.OpError cuya causa raíz es un errno de syscall.
	opErr := func(op string, sysErr syscall.Errno) error {
		return &net.OpError{
			Op:     op,
			Net:    "tcp",
			Addr:   &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443},
			Source: nil,
			Err:    &os.SyscallError{Syscall: "connect", Err: sysErr},
		}
	}

	// Simula un net.OpError con timeout de dial: envolvemos un error que
	// implementa Timeout()=true.
	dialTimeout := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &timeoutErr{},
	}
	readTimeout := &net.OpError{
		Op:  "read",
		Net: "tcp",
		Err: &timeoutErr{},
	}

	tests := []struct {
		name         string
		err          error
		wantCategory Category
		wantRetry    bool
		wantHTTP     int
	}{
		{
			name:         "net.DNSError → dns_resolution",
			err:          &net.DNSError{Name: "example.invalid", IsNotFound: true, Err: "no such host"},
			wantCategory: CategoryDNSResolution,
			wantRetry:    true,
			wantHTTP:     502,
		},
		{
			name:         "syscall.ECONNREFUSED → connection_refused",
			err:          opErr("dial", syscall.ECONNREFUSED),
			wantCategory: CategoryConnectionRefused,
			wantRetry:    true,
			wantHTTP:     502,
		},
		{
			name:         "syscall.ECONNRESET → connection_reset",
			err:          opErr("read", syscall.ECONNRESET),
			wantCategory: CategoryConnectionReset,
			wantRetry:    true,
			wantHTTP:     502,
		},
		{
			name:         "syscall.ENETUNREACH → network_unreachable",
			err:          opErr("dial", syscall.ENETUNREACH),
			wantCategory: CategoryNetworkUnreachable,
			wantRetry:    true,
			wantHTTP:     502,
		},
		{
			name:         "tls.CertificateVerificationError → ssl_error",
			err:          &tls.CertificateVerificationError{Err: errors.New("x509: unknown authority")},
			wantCategory: CategorySSLError,
			wantRetry:    false,
			wantHTTP:     502,
		},
		{
			name:         "dial timeout → timeout_connect",
			err:          dialTimeout,
			wantCategory: CategoryTimeoutConnect,
			wantRetry:    true,
			wantHTTP:     504,
		},
		{
			name:         "read timeout → timeout_read",
			err:          readTimeout,
			wantCategory: CategoryTimeoutRead,
			wantRetry:    true,
			wantHTTP:     504,
		},
		{
			name:         "context.DeadlineExceeded → timeout_read",
			err:          context.DeadlineExceeded,
			wantCategory: CategoryTimeoutRead,
			wantRetry:    true,
			wantHTTP:     504,
		},
		{
			name:         "os.ErrDeadlineExceeded → timeout_read",
			err:          os.ErrDeadlineExceeded,
			wantCategory: CategoryTimeoutRead,
			wantRetry:    true,
			wantHTTP:     504,
		},
		{
			name:         "context.Canceled → unknown (500)",
			err:          context.Canceled,
			wantCategory: CategoryUnknown,
			wantRetry:    true,
			wantHTTP:     500,
		},
		{
			name:         "error genérico → unknown (500)",
			err:          errors.New("boom"),
			wantCategory: CategoryUnknown,
			wantRetry:    true,
			wantHTTP:     500,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.err)
			if got.Category != tc.wantCategory {
				t.Errorf("Category = %q, quiero %q (%v)", got.Category, tc.wantCategory, tc.err)
			}
			if got.IsRetryable != tc.wantRetry {
				t.Errorf("IsRetryable = %v, quiero %v", got.IsRetryable, tc.wantRetry)
			}
			if got.SuggestedHTTPCode != tc.wantHTTP {
				t.Errorf("SuggestedHTTPCode = %d, quiero %d", got.SuggestedHTTPCode, tc.wantHTTP)
			}
			if got.UserMessage == "" {
				t.Errorf("UserMessage vacío")
			}
			if len(got.TroubleshootingSteps) == 0 {
				t.Errorf("TroubleshootingSteps vacío")
			}
		})
	}
}

// timeoutErr es un error auxiliar del test que implementa net.Error con
// Timeout()=true. Sirve para simular *net.OpError de timeout sin abrir sockets.
type timeoutErr struct{}

func (*timeoutErr) Error() string   { return "i/o timeout" }
func (*timeoutErr) Timeout() bool   { return true }
func (*timeoutErr) Temporary() bool { return true }

// descFromException traduce una excepción grabada del corpus al errDesc que
// espera classifyDesc. La tabla es explícita: cada (module, type) del corpus
// aparece aquí, y añadir un tipo nuevo obliga a decidir cómo se clasifica.
func descFromException(tb testing.TB, name string, exc *testutil.Exception) errDesc {
	tb.Helper()
	d := errDesc{
		kind:     exceptionKind(exc),
		typeName: exc.Type,
		message:  exc.Str,
	}
	if exc.Cause != nil {
		cd := descFromException(tb, name, exc.Cause)
		d.cause = &cd
		if cd.kind == kindGaierror && len(exc.Cause.Args) > 0 {
			var errno int
			if err := json.Unmarshal(exc.Cause.Args[0], &errno); err != nil {
				tb.Fatalf("[%s] errno de gaierror: %v", name, err)
			}
			d.errno = errno
		}
	}
	return d
}

func exceptionKind(exc *testutil.Exception) errKind {
	switch exc.Module + "." + exc.Type {
	case "socket.gaierror":
		return kindGaierror
	case "httpx.ConnectError":
		return kindConnectError
	case "httpx.ConnectTimeout":
		return kindConnectTimeout
	case "httpx.ReadTimeout":
		return kindReadTimeout
	case "httpx.TimeoutException":
		return kindTimeoutException
	case "httpx.ProxyError":
		return kindProxyError
	case "httpx.TooManyRedirects":
		return kindTooManyRedirects
	case "httpx.RequestError":
		return kindRequestError
	}
	// Cualquier otro tipo (builtins.Exception incluido) cae en la rama non-httpx.
	return kindOther
}
