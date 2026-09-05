// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package networkerrors clasifica los fallos de transporte que puede sufrir el
// gateway al hablar con la API de Kiro y los transforma en mensajes accionables
// para el usuario final. Es un port literal de kiro/network_errors.py
// (jwadow/kiro-gateway, fijado en el commit a5292ca), verificado contra 40
// casos golden del corpus (27 + 4 + 9).
//
// Arquitectura del port. El original clasifica excepciones de httpx; en Go
// llegan errores de net, net/http, crypto/tls, os y context. La lógica de
// decisión vive en un clasificador interno (classifyDesc) que trabaja sobre una
// DESCRIPCIÓN de la excepción (errDesc), no sobre el error crudo. Así el corpus
// puede verificarla llamando a classifyDesc con una descripción derivada del
// marcador __exception__ del corpus, y la función pública Classify traduce un
// error real de Go a la misma descripción antes de delegar en classifyDesc.
// Esta separación es lo que permite que un corpus grabado con excepciones de
// Python verifique un port que en producción recibe errores de Go.
package networkerrors

// Category clasifica un fallo de transporte. Las diez categorías son las que
// grabó el corpus y no se pueden ampliar sin regenerarlo.
type Category string

const (
	// CategoryDNSResolution: la resolución DNS falló (socket.gaierror
	// encadenado a httpx.ConnectError en el original).
	CategoryDNSResolution Category = "dns_resolution"
	// CategoryConnectionRefused: el servidor rechazó la conexión.
	CategoryConnectionRefused Category = "connection_refused"
	// CategoryConnectionReset: el servidor cerró la conexión inesperadamente.
	CategoryConnectionReset Category = "connection_reset"
	// CategoryNetworkUnreachable: la red del servidor es inalcanzable.
	CategoryNetworkUnreachable Category = "network_unreachable"
	// CategoryTimeoutConnect: se agotó el tiempo del handshake TCP.
	CategoryTimeoutConnect Category = "timeout_connect"
	// CategoryTimeoutRead: se agotó el tiempo de lectura tras conectar.
	CategoryTimeoutRead Category = "timeout_read"
	// CategorySSLError: fallo TLS/SSL o de verificación de certificado.
	CategorySSLError Category = "ssl_error"
	// CategoryProxyError: fallo del proxy configurado.
	CategoryProxyError Category = "proxy_error"
	// CategoryTooManyRedirects: bucle de redirecciones.
	CategoryTooManyRedirects Category = "too_many_redirects"
	// CategoryUnknown: cualquier otro fallo (incluye errores no de red).
	CategoryUnknown Category = "unknown"
)

// Info es la información estructurada de un fallo de red. Los nombres JSON son
// los que grabó el corpus y NO se pueden cambiar. Info contiene un slice, por
// lo que NO es comparable con ==: los tests usan reflect.DeepEqual (o pasan por
// json.Marshal/Unmarshal a `any` cuando el orden de las claves también importa).
type Info struct {
	Category             Category `json:"category"`
	UserMessage          string   `json:"user_message"`
	TroubleshootingSteps []string `json:"troubleshooting_steps"`
	TechnicalDetails     string   `json:"technical_details"`
	IsRetryable          bool     `json:"is_retryable"`
	SuggestedHTTPCode    int      `json:"suggested_http_code"`
}

// errKind identifica la clase de excepción que el clasificador ve. Está pensada
// para que classifyDesc no dependa del tipo real del error (httpx en Python,
// net/http en Go): la función pública Classify traduce el error de Go a errDesc,
// y el test golden traduce el marcador __exception__ del corpus a la misma
// descripción con una tabla explícita en el test (ver networkerrors_test.go,
// función exceptionKind).
type errKind int

const (
	// kindOther: no es un error de httpx.RequestError. En el original cae en
	// la última rama y devuelve `unknown` con HTTP 500 (no 502).
	kindOther errKind = iota
	// kindRequestError: httpx.RequestError genérico.
	kindRequestError
	// kindConnectError: httpx.ConnectError. Requiere subclasificación por la
	// causa (gaierror = DNS) y por subcadenas del str(error).
	kindConnectError
	// kindConnectTimeout: httpx.ConnectTimeout.
	kindConnectTimeout
	// kindReadTimeout: httpx.ReadTimeout.
	kindReadTimeout
	// kindTimeoutException: httpx.TimeoutException que no es Connect ni Read.
	kindTimeoutException
	// kindProxyError: httpx.ProxyError.
	kindProxyError
	// kindTooManyRedirects: httpx.TooManyRedirects.
	kindTooManyRedirects
	// kindGaierror: socket.gaierror. Solo aparece como CAUSA de kindConnectError,
	// no como error de entrada.
	kindGaierror
)

// errDesc es la descripción del error que ve el clasificador. Los campos son
// los que classifyDesc necesita para reconstruir la misma salida que el
// original, incluido el technical_details byte-exact (que en la rama DNS lleva
// el errno de la causa gaierror).
type errDesc struct {
	// kind es el tipo lógico del error.
	kind errKind
	// typeName es el nombre de clase que el original grababa en
	// technical_details (p.ej. "ConnectError"). En Go se rellena con un valor
	// que refleje la clase de fallo, aunque no exista un tipo con ese nombre.
	typeName string
	// message es str(error) del original.
	message string
	// cause es la excepción encadenada, si la había. En este port solo importa
	// la causa gaierror (rama DNS).
	cause *errDesc
	// errno es args[0] de la causa cuando kind de la causa es kindGaierror.
	// Sirve para reconstruir "(errno: N)" en technical_details.
	errno int
}
