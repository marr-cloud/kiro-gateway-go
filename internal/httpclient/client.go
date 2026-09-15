// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package httpclient es el cliente HTTP compartido que habla con la API de
// Kiro: reintentos ante 403/429/5xx y errores de red, backoff exponencial,
// proxy (VPN_PROXY_URL + entorno + SOCKS5) y HTTP/2 desactivado. Port de
// .upstream/kiro/http_client.py (349 líneas), con la resolución de proxy de
// .upstream/main.py:181-205 incorporada (ver el comentario de cabecera de
// proxy.go).
//
// # Divergencia deliberada frente a la organización del original
//
// http_client.py declara EXPLÍCITAMENTE, en el docstring de _get_client
// (líneas 111-113), que FIRST_TOKEN_TIMEOUT "is NOT used here" — se aplica
// en streaming_openai.py vía asyncio.wait_for() envolviendo la lectura del
// primer chunk, una capa de negocio por encima de este cliente. http_client.py
// solo usa STREAMING_READ_TIMEOUT como timeout de lectura httpx (que cubre
// TANTO esperar la respuesta como leer cada chunk, uniformemente).
//
// El spec de este port (§5.5) y el brief de esta tarea piden en cambio que
// RequestWithRetry implemente las TRES capas (conexión/primer token/lectura
// entre chunks) en un único sitio. Se ha optado por seguir el spec: en Go no
// existe el equivalente de streaming_openai.py todavía (llega en fase 5, que
// ni siquiera es parte de este plan de fase 4), y colocar el timeout de
// primer token aquí evita que cada futuro llamador (streaming_openai.go,
// streaming_anthropic.go) tenga que reimplementar su propio wait_for. Se dej
// documentado aquí, byte a byte, para quien retome la fase 5: la SEMÁNTICA
// observable (un primer-token-timeout de 15s con reintentos) es la misma que
// el sistema Python real, solo cambia el fichero donde vive.
//
// # Firma de RequestWithRetry: por qué lleva un parámetro `stream` extra
//
// El original expone `stream: bool = False` como parámetro explícito de
// request_with_retry (http_client.py:176), decidido por cada llamador
// (routes_openai.py, routes_anthropic.py, streaming_openai.py, streaming_core.py,
// account_manager.py — todos pasan stream=True o lo omiten explícitamente).
// La firma que proponía el brief (ctx, req, tp) no deja hueco para esa señal,
// y no se puede inferir de forma fiable a partir de un *http.Request (mismo
// endpoint de Kiro, misma URL, para peticiones streaming y no-streaming). Se
// añade `stream bool` como cuarto parámetro: mismo nombre, mismos tres
// primeros parámetros, mismo tipo de retorno — se interpreta como "mantener
// la forma" en el sentido de las instrucciones de la tarea, no como un
// rediseño de la interfaz.
package httpclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/networkerrors"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// maxRetries y baseRetryDelay son constantes fijas, no configuración: el
// original las declara como constantes de módulo en kiro/config.py:200-204
// (MAX_RETRIES=3, BASE_RETRY_DELAY=1.0), no como variables de entorno. Rigen
// las peticiones no-streaming; las streaming usan en su lugar
// cfg.FirstTokenMaxRetries (configurable via FIRST_TOKEN_MAX_RETRIES),
// exactamente como el original selecciona
// `max_retries = FIRST_TOKEN_MAX_RETRIES if stream else MAX_RETRIES`.
const (
	maxRetries     = 3
	baseRetryDelay = 1 * time.Second
)

// Clock abstrae el tiempo para que los tests de backoff no duerman de
// verdad. Solo cubre las esperas entre reintentos (Sleep/After); los
// timeouts de lectura (primer token / entre chunks) usan reloj real porque
// corren una carrera genuina contra I/O de red — ver
// readFirstChunk/readOnceWithTimeout. After existe además de Sleep porque el
// backoff de 429/5xx necesita poder cortarse si ctx se cancela mientras
// espera (ver waitForRetry); Sleep no deja hueco para eso.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) Sleep(d time.Duration)                  { time.Sleep(d) }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Client es el cliente HTTP compartido. No fija http.Client.Timeout: un
// timeout global cortaría cualquier stream en curso, sea cual sea su
// duración — ver TestTransportSettings y el comentario de RequestWithRetry.
type Client struct {
	cfg        *config.Config
	transport  *http.Transport
	httpClient *http.Client
	clock      Clock
}

// New crea un Client con el reloj real. cfg no se copia: los cambios en el
// *config.Config subyacente después de New (no hay ninguno en este port,
// config.Config es inmutable tras Load) se reflejarían en llamadas
// posteriores a RequestWithRetry.
func New(cfg *config.Config) (*Client, error) {
	return newClientWithClock(cfg, realClock{})
}

// newClientWithClock es la construcción real; New la envuelve con el reloj
// de producción. Vive sin exportar porque el reloj falso es un detalle de
// test, no parte de la API pública del paquete.
func newClientWithClock(cfg *config.Config, clock Clock) (*Client, error) {
	transport, err := buildTransport(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{
		cfg:        cfg,
		transport:  transport,
		httpClient: &http.Client{Transport: transport},
		clock:      clock,
	}, nil
}

// Close libera las conexiones inactivas del Transport. Nunca devuelve error;
// la firma lo declara para que el llamador pueda tratar todos los recursos
// del gateway (auth, accountmanager, httpclient) con la misma forma
// `Close() error`, igual que el spec pide para el resto de fase 4.
func (c *Client) Close() error {
	c.transport.CloseIdleConnections()
	return nil
}

// RequestError envuelve la clasificación de networkerrors cuando
// RequestWithRetry agota los reintentos sin una respuesta utilizable.
// Corresponde a la HTTPException que el original lanza al final de
// request_with_retry (http_client.py:327-341).
type RequestError struct {
	Info networkerrors.Info
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("%s (%s): %s", e.Info.UserMessage, e.Info.Category, e.Info.TechnicalDetails)
}

// forceRefresher es la interfaz opcional que RequestWithRetry busca en el
// TokenProvider recibido para el camino de 403. utils.TokenProvider (fase 2)
// solo expone AccessToken/ProfileARN — no tiene forma de FORZAR un refresco.
// auth.Manager (fase 4, Task 5) expondrá ForceRefresh(ctx) con exactamente
// esta forma; en cuanto exista satisface esta interfaz de forma estructural,
// sin que este paquete lo importe ni lo sepa. Si el TokenProvider recibido no
// la implementa, el 403 igualmente repite la petición (llama a
// GetKiroHeaders de nuevo, que llama a AccessToken) — sin garantía de que el
// token cambie, que es el máximo que se puede hacer con la interfaz mínima.
type forceRefresher interface {
	ForceRefresh(ctx context.Context) (string, error)
}

func forceRefreshToken(ctx context.Context, tp utils.TokenProvider) {
	if fr, ok := tp.(forceRefresher); ok {
		_, _ = fr.ForceRefresh(ctx)
	}
}

// RequestWithRetry hace la petición con reintentos calcados del original
// (http_client.py:170-343):
//
//   - 403: fuerza refresco de token (forceRefresher, ver arriba) y repite,
//     sin consumir backoff.
//   - 429 y 5xx: espera baseRetryDelay×2^intento (1s, 2s, 4s...) y repite,
//     SIN condición — incluso en el último intento, igual que el original
//     (http_client.py:247-261 duerme sin comprobar si quedan intentos; el
//     bucle deja de iterar porque range(max_retries) se agota, no porque se
//     salte la espera). Agotados los intentos, devuelve la ÚLTIMA respuesta
//     tal cual (como el "last_response" del original, con el body
//     re-materializado en memoria porque en Go, a diferencia de httpx en
//     modo no-streaming, el body no se buferiza solo), no un error.
//   - Errores de transporte (DNS, TLS, timeout, rechazo...): se clasifican
//     con networkerrors.Classify; si es reintentable y quedan intentos,
//     espera y repite; si no, devuelve un *RequestError de inmediato.
//   - stream=true: el presupuesto de intentos es cfg.FirstTokenMaxRetries
//     (no maxRetries), se añade la cabecera Connection: close (mitigación
//     CLOSE_WAIT, issue #38 del original), y una vez la respuesta es 200 se
//     exige que el PRIMER Read() del body llegue dentro de
//     cfg.FirstTokenTimeout — si no, se descarta la respuesta y se reintenta
//     la petición completa (nueva conexión, cabeceras frescas). Los Read()
//     siguientes (una vez pasado el primero) renuevan un deadline de
//     cfg.StreamingReadTimeout en cada llamada.
//   - stream=false: sin envoltorio de timeouts en el body; el llamador lee y
//     cierra el *http.Response.Body normal.
//
// req debe llevar GetBody si su Body no está vacío y se quiere que los
// reintentos reenvíen el mismo cuerpo (http.NewRequest ya lo hace solo para
// *bytes.Reader/*bytes.Buffer/*strings.Reader, el caso común de un payload
// JSON). Las cabeceras de req se REEMPLAZAN por completo en cada intento con
// utils.GetKiroHeaders(ctx, tp): el original tampoco fusiona cabeceras del
// llamador, construye el mapa desde cero en cada intento.
func (c *Client) RequestWithRetry(ctx context.Context, req *http.Request, tp utils.TokenProvider, stream bool) (*http.Response, error) {
	maxAttempts := maxRetries
	if stream {
		maxAttempts = c.cfg.FirstTokenMaxRetries
	}
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	// lastRetryableResp guarda la última respuesta 429/5xx, re-materializada
	// en memoria (ver bufferAndClose), por si se agotan los intentos: el
	// original la devuelve tal cual (last_response) en vez de sintetizar un
	// error, y el llamador necesita ver el código/cuerpo real de Kiro.
	var lastRetryableResp *http.Response

	for attempt := 0; attempt < maxAttempts; attempt++ {
		attemptReq, err := c.prepareAttempt(ctx, req, tp, stream)
		if err != nil {
			return nil, err
		}

		resp, err := c.httpClient.Do(attemptReq)
		if err != nil {
			info := networkerrors.Classify(err)
			if info.IsRetryable && attempt < maxAttempts-1 {
				c.clock.Sleep(backoffDelay(attempt))
				continue
			}
			return nil, &RequestError{Info: info}
		}

		switch {
		case resp.StatusCode == http.StatusOK:
			if !stream {
				return resp, nil
			}
			firstChunk, ferr := readFirstChunk(resp.Body, c.firstTokenTimeout())
			if ferr != nil {
				_ = resp.Body.Close()
				info := networkerrors.Classify(ferr)
				if attempt < maxAttempts-1 {
					c.clock.Sleep(backoffDelay(attempt))
					continue
				}
				return nil, &RequestError{Info: info}
			}
			resp.Body = &streamBody{prefix: firstChunk, rc: resp.Body, timeout: c.streamingReadTimeout()}
			return resp, nil

		case resp.StatusCode == http.StatusForbidden:
			drainAndClose(resp.Body)
			forceRefreshToken(ctx, tp)
			continue

		case resp.StatusCode == http.StatusTooManyRequests ||
			(resp.StatusCode >= 500 && resp.StatusCode < 600):
			buffered, berr := bufferAndClose(resp)
			if berr != nil {
				return nil, &RequestError{Info: networkerrors.Classify(berr)}
			}
			lastRetryableResp = buffered
			if err := c.waitForRetry(ctx, attempt); err != nil {
				return nil, err
			}
			continue

		default:
			return resp, nil
		}
	}

	if lastRetryableResp != nil {
		return lastRetryableResp, nil
	}

	// Solo se llega aquí si TODOS los intentos fueron 403 (ningún otro
	// camino del switch deja que el bucle termine sin devolver ni sin fijar
	// lastRetryableResp). El original, en ese caso, tampoco tiene
	// last_response ni last_error_info y cae en el HTTPException genérico
	// de las líneas 331-342, que además distingue 504 (stream) de 502
	// (no-stream).
	return nil, &RequestError{Info: exhaustedInfo(stream)}
}

// waitForRetry espera baseRetryDelay×2^attempt antes de reintentar un
// 429/5xx, pero se corta de inmediato si ctx se cancela mientras espera —
// a diferencia de un Sleep() plano, que ignoraría la cancelación hasta que
// el propio delay terminase. Solo se usa en el camino 429/5xx: el original
// SOLO duerme sin condición en ese camino (http_client.py:247-261); las
// ramas de excepción (timeout/error de red, y el timeout de primer token de
// este port) sí comprueban `attempt < max_retries - 1` antes de dormir
// (http_client.py:276, 295), así que esas dos siguen usando Sleep con guarda.
func (c *Client) waitForRetry(ctx context.Context, attempt int) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.clock.After(backoffDelay(attempt)):
		return nil
	}
}

// bufferAndClose lee el body completo de resp, lo cierra, y lo sustituye por
// un io.NopCloser sobre los bytes ya leídos: httpx en modo no-streaming
// buferiza el body automáticamente dentro de client.request(), así que
// guardar "last_response" allí es gratis; en Go el body es siempre un
// stream vivo, así que hay que materializarlo a mano para poder conservarlo
// más allá del intento que lo produjo (y para poder cerrarlo antes de que el
// siguiente intento abra una conexión nueva).
func bufferAndClose(resp *http.Response) (*http.Response, error) {
	body, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

func (c *Client) firstTokenTimeout() time.Duration {
	return durationFromSeconds(c.cfg.FirstTokenTimeout)
}

func (c *Client) streamingReadTimeout() time.Duration {
	return durationFromSeconds(c.cfg.StreamingReadTimeout)
}

func durationFromSeconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

func backoffDelay(attempt int) time.Duration {
	return baseRetryDelay * time.Duration(uint(1)<<uint(attempt))
}

// prepareAttempt construye la petición de un intento: cabeceras frescas de
// utils.GetKiroHeaders (el token puede haber cambiado tras un 403), un body
// nuevo vía GetBody si existe, y Connection: close si stream.
func (c *Client) prepareAttempt(ctx context.Context, req *http.Request, tp utils.TokenProvider, stream bool) (*http.Request, error) {
	hdr, err := utils.GetKiroHeaders(ctx, tp)
	if err != nil {
		return nil, fmt.Errorf("obteniendo cabeceras Kiro: %w", err)
	}
	if stream {
		hdr.Set("Connection", "close")
	}

	attemptReq := req.Clone(ctx)
	attemptReq.Header = hdr
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("releyendo el body de la petición para reintento: %w", err)
		}
		attemptReq.Body = body
	}
	return attemptReq, nil
}

func drainAndClose(rc io.ReadCloser) {
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
}

// exhaustedInfo replica el HTTPException genérico de http_client.py:331-342
// para el caso "no hay last_response ni last_error_info" (en este port:
// todos los intentos fueron 403). El original distingue 504 para streaming
// de 502 para no-streaming; antes de este fix el port devolvía 502 siempre,
// ignorando stream.
func exhaustedInfo(stream bool) networkerrors.Info {
	code := 502
	msg := "Request failed after exhausting all retry attempts."
	if stream {
		code = 504
		msg = "Streaming failed after exhausting all retry attempts."
	}
	return networkerrors.Info{
		Category:          networkerrors.CategoryUnknown,
		UserMessage:       msg,
		TechnicalDetails:  "every attempt returned 403 Forbidden or produced no usable response",
		IsRetryable:       false,
		SuggestedHTTPCode: code,
	}
}
