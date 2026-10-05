// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/sse"
)

// Pings SSE durante el streaming. Divergencia intencional con el original
// (DIFFERENCES §14): upstream define PingEvent pero nunca lo emite. Claude Code
// aborta un stream cuando no le llegan bytes durante su idle timeout (300 s),
// y Kiro puede callar ese tiempo en un thinking largo.

// defaultPingInterval es el intervalo entre pings en producción.
const defaultPingInterval = 15 * time.Second

// pingEventBytes es el evento ping de Anthropic, con los separadores de
// json.dumps del resto de eventos.
var pingEventBytes = sse.FormatEvent("ping", []byte(`{"type": "ping"}`))

// syncStreamWriter serializa las escrituras del pipeline y las del pinger sobre
// el mismo ResponseWriter. Basta con bloquear por Write porque cada evento SSE
// se escribe con un único Write (streaminganthropic.writeEvent).
type syncStreamWriter struct {
	mu      sync.Mutex
	w       io.Writer
	flusher http.Flusher
	started bool
}

func (s *syncStreamWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = true
	return s.w.Write(p)
}

func (s *syncStreamWriter) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flusher.Flush()
}

// ping escribe un evento ping, solo después del primer evento del stream
// (message_start tiene que ser el primero que ve el cliente).
func (s *syncStreamWriter) ping() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return
	}
	if _, err := s.w.Write(pingEventBytes); err == nil {
		s.flusher.Flush()
	}
}

// startPinger emite un ping cada interval hasta que se llama a la función
// devuelta, que espera a que la goroutine termine: así nada escribe en el
// ResponseWriter después de que el handler retorne.
func startPinger(s *syncStreamWriter, interval time.Duration) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				s.ping()
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}
