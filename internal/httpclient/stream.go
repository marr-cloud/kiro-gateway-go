// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package httpclient

import (
	"context"
	"io"
	"time"
)

// readFirstChunk hace UN intento de lectura sobre rc con un límite de
// timeout de pared real (no el Clock inyectable: esto corre una carrera
// genuina contra I/O de red, el Clock solo cubre las esperas de backoff
// entre reintentos). Un EOF inmediato (body vacío) se trata como éxito, no
// como fallo: el streamBody resultante volverá a ver EOF en su primera
// lectura real, que es un comportamiento válido de io.Reader.
//
// Devuelve context.DeadlineExceeded si el timeout vence antes de que rc.Read
// produzca nada; ese error es exactamente lo que
// networkerrors.translate() reconoce como un timeout genérico reintentable
// (ver internal/networkerrors/classify.go).
func readFirstChunk(rc io.Reader, timeout time.Duration) ([]byte, error) {
	buf := make([]byte, 32*1024)
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := rc.Read(buf)
		ch <- result{n: n, err: err}
	}()

	select {
	case r := <-ch:
		if r.err != nil && r.err != io.EOF {
			return nil, r.err
		}
		chunk := make([]byte, r.n)
		copy(chunk, buf[:r.n])
		return chunk, nil
	case <-time.After(timeout):
		return nil, context.DeadlineExceeded
	}
}

// readOnceWithTimeout hace un único Read con el mismo patrón de carrera que
// readFirstChunk, pero copiando a un buffer PRIVADO dentro de la goroutine
// en vez de escribir directamente en p: si el timeout gana la carrera, la
// goroutine (que puede seguir bloqueada en rc.Read durante un rato más)
// nunca llega a tocar el slice p del llamador, evitando una escritura
// concurrente sobre un buffer que el llamador ya cree suyo de nuevo.
func readOnceWithTimeout(rc io.Reader, p []byte, timeout time.Duration) (int, error) {
	priv := make([]byte, len(p))
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := rc.Read(priv)
		ch <- result{n: n, err: err}
	}()

	select {
	case r := <-ch:
		copy(p, priv[:r.n])
		return r.n, r.err
	case <-time.After(timeout):
		return 0, context.DeadlineExceeded
	}
}

// streamBody envuelve el body de una respuesta de streaming: el primer
// chunk, ya leído por RequestWithRetry bajo el timeout de primer token, se
// devuelve antes de seguir leyendo rc; cada lectura posterior renueva un
// deadline de timeout (StreamingReadTimeout), replicando "lectura entre
// chunks" del spec §5.5.
type streamBody struct {
	prefix  []byte
	rc      io.ReadCloser
	timeout time.Duration
}

func (s *streamBody) Read(p []byte) (int, error) {
	if len(s.prefix) > 0 {
		n := copy(p, s.prefix)
		s.prefix = s.prefix[n:]
		return n, nil
	}
	return readOnceWithTimeout(s.rc, p, s.timeout)
}

func (s *streamBody) Close() error {
	return s.rc.Close()
}
