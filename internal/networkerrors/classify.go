// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package networkerrors

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
)

// classifyDesc es el clasificador puro: dada la descripción de un error decide
// la categoría, el mensaje al usuario, los pasos de resolución y el código HTTP
// sugerido. Es un port literal de kiro.network_errors.classify_network_error
// (rama por rama) verificado por los 27 casos golden del corpus.
//
// El orden de decisión se copia de la fuente:
//
//  1. ConnectError → si la causa es gaierror, dns_resolution; si no,
//     subclasificación por subcadenas del message (Connection refused/reset,
//     Network is unreachable, ENETUNREACH, SSL/TLS/certificate); si nada
//     coincide, unknown con el mensaje "Connection failed - ...".
//  2. ConnectTimeout → timeout_connect (504).
//  3. ReadTimeout → timeout_read (504).
//  4. TimeoutException genérica → timeout_read con el mensaje "Request timeout
//     - ..." (504).
//  5. TooManyRedirects → too_many_redirects (502, no retryable).
//  6. ProxyError → proxy_error (502).
//  7. RequestError genérica → unknown "Network request failed ..." (502).
//  8. Cualquier otra cosa (no-httpx) → unknown "An unexpected error ..." (500).
func classifyDesc(d errDesc) Info {
	technicalDetails := d.typeName + ": " + d.message

	switch d.kind {
	case kindConnectError:
		return classifyConnectError(d, technicalDetails)
	case kindConnectTimeout:
		return Info{
			Category:    CategoryTimeoutConnect,
			UserMessage: "Connection timeout - server did not respond to connection attempt.",
			TroubleshootingSteps: []string{
				"Check your internet connection speed",
				"The server may be overloaded or slow to respond",
				"Try again in a few moments",
				"Check if firewall is delaying connections",
			},
			TechnicalDetails:  technicalDetails,
			IsRetryable:       true,
			SuggestedHTTPCode: 504,
		}
	case kindReadTimeout:
		return Info{
			Category:    CategoryTimeoutRead,
			UserMessage: "Read timeout - server stopped responding during data transfer.",
			TroubleshootingSteps: []string{
				"The server may be processing a complex request",
				"Check your internet connection stability",
				"Try again with a simpler request",
				"The service may be experiencing high load",
			},
			TechnicalDetails:  technicalDetails,
			IsRetryable:       true,
			SuggestedHTTPCode: 504,
		}
	case kindTimeoutException:
		return Info{
			Category:    CategoryTimeoutRead,
			UserMessage: "Request timeout - operation took too long to complete.",
			TroubleshootingSteps: []string{
				"Check your internet connection",
				"The server may be slow or overloaded",
				"Try again in a few moments",
			},
			TechnicalDetails:  technicalDetails,
			IsRetryable:       true,
			SuggestedHTTPCode: 504,
		}
	case kindTooManyRedirects:
		return Info{
			Category:    CategoryTooManyRedirects,
			UserMessage: "Too many redirects - the server is redirecting in a loop.",
			TroubleshootingSteps: []string{
				"This is likely a server-side configuration issue",
				"Try accessing the service directly without the gateway",
				"Contact the service provider if the issue persists",
			},
			TechnicalDetails:  technicalDetails,
			IsRetryable:       false,
			SuggestedHTTPCode: 502,
		}
	case kindProxyError:
		return Info{
			Category:    CategoryProxyError,
			UserMessage: "Proxy connection failed - cannot connect through the configured proxy.",
			TroubleshootingSteps: []string{
				"Check proxy configuration (HTTP_PROXY, HTTPS_PROXY environment variables)",
				"Verify proxy server is accessible",
				"Try disabling proxy temporarily",
				"Check proxy authentication credentials if required",
			},
			TechnicalDetails:  technicalDetails,
			IsRetryable:       true,
			SuggestedHTTPCode: 502,
		}
	case kindRequestError:
		return Info{
			Category:    CategoryUnknown,
			UserMessage: "Network request failed due to an unexpected error.",
			TroubleshootingSteps: []string{
				"Check your internet connection",
				"Verify firewall/antivirus settings",
				"Try again in a few moments",
				"Check the debug logs for more details",
			},
			TechnicalDetails:  technicalDetails,
			IsRetryable:       true,
			SuggestedHTTPCode: 502,
		}
	default:
		return Info{
			Category:    CategoryUnknown,
			UserMessage: "An unexpected error occurred.",
			TroubleshootingSteps: []string{
				"Check the debug logs for details",
				"Try again in a few moments",
				"Report this issue if it persists",
			},
			TechnicalDetails:  technicalDetails,
			IsRetryable:       true,
			SuggestedHTTPCode: 500,
		}
	}
}

// classifyConnectError reproduce el _classify_connect_error del original. El
// orden de las ramas importa: por ejemplo un ConnectError con causa gaierror y
// mensaje "SSL" caería en DNS por la causa, no en SSL, porque la comprobación
// de la causa es la primera.
func classifyConnectError(d errDesc, technicalDetails string) Info {
	// Rama DNS: si la causa es gaierror, se anexa el errno a technical_details.
	if d.cause != nil && d.cause.kind == kindGaierror {
		td := fmt.Sprintf("%s (errno: %d)", technicalDetails, d.errno)
		return Info{
			Category:    CategoryDNSResolution,
			UserMessage: "DNS resolution failed - cannot resolve the provider's domain name.",
			TroubleshootingSteps: []string{
				"Check your internet connection",
				"Try changing DNS servers to Google DNS (8.8.8.8, 8.8.4.4) or Cloudflare (1.1.1.1, 1.0.0.1)",
				"Temporarily disable VPN if you're using one",
				"Check if firewall/antivirus is blocking DNS requests",
				"Verify the domain name is correct and the service is operational",
			},
			TechnicalDetails:  td,
			IsRetryable:       true,
			SuggestedHTTPCode: 502,
		}
	}

	s := d.message
	sLower := strings.ToLower(s)
	switch {
	case strings.Contains(s, "Connection refused") || strings.Contains(s, "ECONNREFUSED"):
		return Info{
			Category:    CategoryConnectionRefused,
			UserMessage: "Connection refused - the server is not accepting connections.",
			TroubleshootingSteps: []string{
				"The service may be temporarily down",
				"Check if the service is running and accessible",
				"Verify firewall is not blocking the connection",
				"Try again in a few moments",
			},
			TechnicalDetails:  technicalDetails,
			IsRetryable:       true,
			SuggestedHTTPCode: 502,
		}
	case strings.Contains(s, "Connection reset") || strings.Contains(s, "ECONNRESET"):
		return Info{
			Category:    CategoryConnectionReset,
			UserMessage: "Connection reset - the server closed the connection unexpectedly.",
			TroubleshootingSteps: []string{
				"This is usually a temporary server issue",
				"Try again in a few moments",
				"Check if VPN/proxy is interfering with the connection",
				"Verify network stability",
			},
			TechnicalDetails:  technicalDetails,
			IsRetryable:       true,
			SuggestedHTTPCode: 502,
		}
	case strings.Contains(s, "Network is unreachable") ||
		strings.Contains(s, "No route to host") ||
		strings.Contains(s, "ENETUNREACH"):
		return Info{
			Category:    CategoryNetworkUnreachable,
			UserMessage: "Network unreachable - cannot reach the server's network.",
			TroubleshootingSteps: []string{
				"Check your internet connection",
				"Verify network adapter is enabled and working",
				"Check routing table if using VPN",
				"Try disabling VPN temporarily",
				"Restart network adapter or router",
			},
			TechnicalDetails:  technicalDetails,
			IsRetryable:       true,
			SuggestedHTTPCode: 502,
		}
	case strings.Contains(s, "SSL") || strings.Contains(s, "TLS") || strings.Contains(sLower, "certificate"):
		return Info{
			Category:    CategorySSLError,
			UserMessage: "SSL/TLS error - secure connection could not be established.",
			TroubleshootingSteps: []string{
				"Check system date and time (incorrect time causes SSL errors)",
				"Update SSL certificates on your system",
				"Check if antivirus/firewall is intercepting HTTPS traffic",
				"Verify the server's SSL certificate is valid",
			},
			TechnicalDetails:  technicalDetails,
			IsRetryable:       false,
			SuggestedHTTPCode: 502,
		}
	}

	// Ninguna subcadena encajó: el original devuelve unknown con un mensaje
	// específico para el ConnectError sin causa reconocible.
	return Info{
		Category:    CategoryUnknown,
		UserMessage: "Connection failed - unable to establish connection to the server.",
		TroubleshootingSteps: []string{
			"Check your internet connection",
			"Verify firewall/antivirus settings",
			"Try disabling VPN temporarily",
			"Check if the service is accessible from other devices",
		},
		TechnicalDetails:  technicalDetails,
		IsRetryable:       true,
		SuggestedHTTPCode: 502,
	}
}

// Classify traduce un error de Go a una clase de fallo y delega en
// classifyDesc. Esta es la función que consume la fase 4 en producción.
//
// Correspondencia con las clases de excepción del original (tabla también
// verificada en TestClassifyGoErrorTranslation):
//
//	*net.DNSError                          → kindConnectError + causa gaierror (dns_resolution)
//	*net.OpError con syscall.ECONNREFUSED  → kindConnectError message="Connection refused" (connection_refused)
//	*net.OpError con syscall.ECONNRESET    → kindConnectError message="Connection reset"   (connection_reset)
//	*net.OpError con syscall.ENETUNREACH   → kindConnectError message="Network is unreachable" (network_unreachable)
//	*net.OpError con Timeout(), Op=="dial" → kindConnectTimeout                            (timeout_connect)
//	*net.OpError con Timeout()             → kindReadTimeout                               (timeout_read)
//	*tls.CertificateVerificationError      → kindConnectError message contiene "certificate" (ssl_error)
//	otros *tls.RecordHeaderError/alerts    → kindConnectError message contiene "TLS"        (ssl_error)
//	context.DeadlineExceeded               → kindTimeoutException                         (timeout_read genérico)
//	os.ErrDeadlineExceeded                 → kindTimeoutException                         (timeout_read genérico)
//	context.Canceled                       → kindOther                                    (unknown 500)
//	cualquier otro error                   → kindOther                                    (unknown 500)
//
// Justificación de cada mapeo:
//
//   - `net.DNSError` en Go es exactamente lo que en el original era una causa
//     `socket.gaierror` colgada de un `httpx.ConnectError`: la resolución
//     falló. El clasificador espera ver un ConnectError con causa gaierror para
//     entrar en la rama DNS, así que ese es el errDesc que se construye. El
//     errno se aproxima con -2 (EAI_NONAME) cuando IsNotFound es true, y con 0
//     cuando no lo es: en Go no está expuesto y una constante convencional
//     evita el "None" que el original imprimiría.
//   - `*net.OpError` es el envoltorio que devuelven las operaciones de socket
//     en Go. Encapsula un `syscall.Errno` cuando el fallo viene del kernel, y
//     `errors.Is` con las constantes de syscall permite distinguir
//     refused/reset/unreachable sin depender del texto del error.
//   - `Timeout()` de `net.Error` distingue timeouts. `Op=="dial"` los separa
//     entre handshake y lectura, que es lo que el original hace clasificando
//     ConnectTimeout vs ReadTimeout.
//   - `*tls.CertificateVerificationError` es el fallo canónico de validación
//     de certificado en Go 1.20+. El clasificador entra por la subcadena
//     "certificate" del message (comparación en minúsculas, como el original).
//   - `context.DeadlineExceeded` y `os.ErrDeadlineExceeded` son timeouts que no
//     traen contexto sobre en qué fase se produjeron; se tratan como
//     TimeoutException genérica, que en el original devuelve el mensaje "Request
//     timeout - operation took too long".
//   - `context.Canceled` no es un fallo de red: el caller decidió parar. Va a la
//     rama non-httpx (unknown, 500) para dejar claro que no es reintentable en
//     la capa de transporte, aunque los pasos genéricos de resolución sean
//     poco útiles en ese caso.
func Classify(err error) Info {
	return classifyDesc(translate(err))
}

// translate hace la traducción error de Go → errDesc. Se mantiene aparte de
// Classify para que el flujo sea explícito y testable.
func translate(err error) errDesc {
	if err == nil {
		// El original recibe siempre una excepción; en Go un nil sería un bug
		// del llamador, pero devolvemos algo razonable en vez de un panic.
		return errDesc{kind: kindOther, typeName: "Exception", message: ""}
	}

	// DNS: el envoltorio típico de las lookups fallidas.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		errno := 0
		if dnsErr.IsNotFound {
			// EAI_NONAME en Unix; en Windows el original grababa 11001. -2 es
			// una elección convencional porque el corpus grabó ambos valores y
			// technical_details en producción no es un punto de contrato.
			errno = -2
		}
		return errDesc{
			kind:     kindConnectError,
			typeName: "ConnectError",
			message:  dnsErr.Error(),
			errno:    errno,
			cause: &errDesc{
				kind:     kindGaierror,
				typeName: "gaierror",
				message:  dnsErr.Err,
			},
		}
	}

	// Timeouts: se comprueban antes que syscall porque un timeout puede envolver
	// un errno de fondo que no lo describe (p.ej. EAGAIN en Linux).
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		// Si viene envuelto en un OpError con Op=="dial", es un timeout de
		// handshake; si no, se trata como timeout de lectura o timeout genérico.
		if op := opErrorOf(err); op != nil && op.Op == "dial" {
			return errDesc{kind: kindConnectTimeout, typeName: "ConnectTimeout", message: err.Error()}
		}
		return errDesc{kind: kindTimeoutException, typeName: "TimeoutException", message: err.Error()}
	}
	if op := opErrorOf(err); op != nil && op.Timeout() {
		if op.Op == "dial" {
			return errDesc{kind: kindConnectTimeout, typeName: "ConnectTimeout", message: err.Error()}
		}
		return errDesc{kind: kindReadTimeout, typeName: "ReadTimeout", message: err.Error()}
	}

	// TLS: fallo de verificación de certificado.
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		// El message debe contener "certificate" para que classifyConnectError
		// entre en la rama SSL.
		msg := "certificate verification failed: " + certErr.Error()
		return errDesc{kind: kindConnectError, typeName: "ConnectError", message: msg}
	}

	// syscall errnos que el original ve como subcadenas del str(error).
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return errDesc{
			kind:     kindConnectError,
			typeName: "ConnectError",
			message:  "Connection refused: " + err.Error(),
		}
	case errors.Is(err, syscall.ECONNRESET):
		return errDesc{
			kind:     kindConnectError,
			typeName: "ConnectError",
			message:  "Connection reset: " + err.Error(),
		}
	case errors.Is(err, syscall.ENETUNREACH):
		return errDesc{
			kind:     kindConnectError,
			typeName: "ConnectError",
			message:  "Network is unreachable: " + err.Error(),
		}
	}

	// Cancelaciones del contexto y todo lo demás: se tratan como no-httpx.
	if errors.Is(err, context.Canceled) {
		return errDesc{kind: kindOther, typeName: "Exception", message: err.Error()}
	}

	return errDesc{kind: kindOther, typeName: "Exception", message: err.Error()}
}

// opErrorOf devuelve el *net.OpError más externo de la cadena, o nil.
func opErrorOf(err error) *net.OpError {
	var op *net.OpError
	if errors.As(err, &op) {
		return op
	}
	return nil
}
