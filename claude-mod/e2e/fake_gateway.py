"""Gateway falso para el e2e del mod kiro: /health, /kiro/status y
/v1/messages en SSE. claude-sonnet-5-5 corta con stop_reason "refusal"; el
resto responde "ok". Registra en el log el modelo de cada /v1/messages.

Resultado medido (Claude Code 2.1.289, con el hook de reintento):
- el respaldo responde: 2 peticiones (original + respaldo).
- todo corta (modo todo): 3 peticiones (original, respaldo y el reintento propio de CC,
  que el hook ya no vuelve a reintentar). Sin la guarda eran 4.
- corte a mitad (modo parcial): 2 peticiones; result "ok", pero el transcript conserva el
  mensaje "parcial " del intento cortado y a continuacion el "ok" del respaldo.

Uso: uv run --no-project python fake_gateway.py <puerto> <log> [todo|parcial]
  todo: TODOS los modelos cortan con refusal.
  parcial: los que cortan emiten antes el texto "parcial " (corte a mitad, como CONTENT_FILTERED).
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1])
LOG = open(sys.argv[2], "a", buffering=1, encoding="utf-8")
MODE = sys.argv[3] if len(sys.argv) > 3 else ""
REFUSE = "claude-sonnet-5-5"
STATUS = {
    "version": "e2e", "uptime_seconds": 1.0, "active_account": "e2e",
    "debug": {"mode": "off", "dir": "C:\\e2e\\debug_logs"},
    "models": [
        {"id": "claude-sonnet-5.5", "native_thinking": ["adaptive"], "effort_levels": [], "refusal_fallback": "claude-sonnet-5"},
        {"id": "claude-sonnet-5", "native_thinking": ["adaptive"], "effort_levels": [], "refusal_fallback": ""},
    ],
}


def sse(event, data):
    return f"event: {event}\ndata: {json.dumps(data)}\n\n".encode()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def send_json(self, code, body):
        raw = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        if self.path == "/health":
            return self.send_json(200, {"status": "healthy"})
        if self.path == "/kiro/status":
            return self.send_json(200, STATUS)
        self.send_json(404, {})

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("content-length", 0))) or b"{}")
        if not self.path.startswith("/v1/messages") or "count_tokens" in self.path:
            return self.send_json(404, {})
        model = body.get("model", "")
        LOG.write(f"model={model}\n")
        refuse = MODE == "todo" or model.lower().replace(".", "-") == REFUSE
        self.send_response(200)
        self.send_header("content-type", "text/event-stream")
        self.end_headers()
        msg = {"id": "msg_e2e", "type": "message", "role": "assistant", "model": model, "content": [],
               "stop_reason": None, "stop_sequence": None, "usage": {"input_tokens": 5, "output_tokens": 0}}
        out = sse("message_start", {"type": "message_start", "message": msg})
        if not refuse or MODE == "parcial":
            text = "parcial " if refuse else "ok"
            out += sse("content_block_start", {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}})
            out += sse("content_block_delta", {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": text}})
            out += sse("content_block_stop", {"type": "content_block_stop", "index": 0})
        out += sse("message_delta", {"type": "message_delta",
                                     "delta": {"stop_reason": "refusal" if refuse else "end_turn", "stop_sequence": None},
                                     "usage": {"output_tokens": 1}})
        out += sse("message_stop", {"type": "message_stop"})
        self.wfile.write(out)


ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
