# Nivel 3: mod de Claude Code para kiro-gateway — diseño

Fecha: 2026-10-05. Estado: aprobado en chat (enfoque 1), pendiente de revisión escrita.

## Objetivo

Que trabajar con Claude Code sobre Kiro se sienta natural: los fallos no cortan
el trabajo y el gateway se controla sin salir de la sesión.

Lo que pidió el usuario:

- Entrada: se queda `kclaude` (no tocar settings globales ni el comando `claude`).
- Fallos que cortan el trabajo: un refusal reintenta solo con el modelo de
  respaldo de Kiro; un gateway caído se relanza sin salir.
- Controlar el gateway con `/kiro`: estado, reiniciar, logs y debug, modelos.
- Fuera de alcance: barra de estado permanente.

Restricciones: cuidar la cuenta de Kiro (mínimas peticiones extra), comodidad
primero, ninguna key literal en archivos o comandos.

## Límites de los mods (CC 2.1.289, verificados en los tipos)

- Un mod no cambia el base URL del cliente de la API: `ANTHROPIC_BASE_URL` tiene
  que estar en el entorno antes de arrancar `claude` (lo pone `kclaude`).
- Un mod no puede hacer de proveedor: `turn.step` no trae system prompt ni tools.
- Sí puede: reescribir `model` en `turn.step` y ver su `stopReason`, llamar
  `next(e)` otra vez, registrar comandos, `$.http.fetch`, `$.process.run`,
  `$.fs`, `$.env.get`, `$.ui.toast`.

## Piezas

### 1. `scripts/kiro-gateway.ps1`

Única lógica de arranque del gateway, compartida por `kclaude` y el mod.

```
kiro-gateway.ps1 start   [-DebugMode all|errors|off] [-Port 8000]
kiro-gateway.ps1 stop    [-Port 8000]
kiro-gateway.ps1 restart [-DebugMode all|errors|off] [-Port 8000]
```

- `start`: si `/health` responde no hace nada; si no, lanza `kiro-gateway.exe`
  oculto y separado (sobrevive a `claude`), con logs en
  `%LOCALAPPDATA%\kiro-gateway\gateway.log` / `gateway.err.log`, y espera hasta
  20 s a `/health`. `-DebugMode` se pasa como `DEBUG_MODE` solo a ese proceso.
- `stop`: localiza el proceso que escucha en el puerto
  (`Get-NetTCPConnection -LocalPort`), comprueba que es `kiro-gateway` y lo para.
- `restart`: `stop` + `start`.
- Salida: una línea legible y código de salida 0/1, para que el mod la muestre.
- `kclaude` (`scripts/kiro-claude.ps1`) deja de tener su propia lógica de
  arranque, llama a `kiro-gateway.ps1 start` y añade
  `--plugin-dir <repo>\claude-mod`.

### 2. Gateway: `GET /kiro/status`

Protegido con `PROXY_API_KEY` (Bearer o `x-api-key`, como la ruta Anthropic).
Respuesta:

```json
{
  "version": "v0.4.0-…",
  "uptime_seconds": 1234.5,
  "active_account": "…",
  "debug": { "mode": "off", "dir": "C:\\…\\debug_logs" },
  "models": [
    {
      "id": "claude-sonnet-5-5",
      "native_thinking": ["adaptive", "between_tools"],
      "effort_levels": [],
      "refusal_fallback": "claude-sonnet-5"
    }
  ]
}
```

- `debug.dir` absoluto (resuelto desde el directorio de trabajo del gateway).
- `models` sale de lo que guardó el discovery: ids, `modelcaps` y el nuevo
  respaldo por refusal.
- Discovery (`accountmanager/modeldiscovery.go`) pasa a leer también
  `refusalFallbackModels` de cada modelo de `ListAvailableModels` y lo guarda
  junto a sus capacidades. La forma exacta del campo se confirma con una
  respuesta real en la primera tarea del plan antes de escribir el parser.
- Sin efecto en las rutas existentes. DIFFERENCES §20 (endpoint sin
  equivalente en el original).

### 3. Mod `claude-mod/` (en el repo)

Archivos: `.claude-plugin/plugin.json`, `hooks/hooks.json`,
`hooks/register.ts`, tests `*.test.ts`. Nombre del plugin: `kiro`.

**Arranque (`session.start`).** Lee `ANTHROPIC_BASE_URL` y
`ANTHROPIC_AUTH_TOKEN` con `$.env.get`. Si el base URL no es un gateway local
(no empieza por `http://127.0.0.1:` o `http://localhost:`), el mod queda inerte.
Registra `/kiro`. Pide `/kiro/status` una vez y guarda el mapa de respaldos en
`$.state` (se refresca con `/kiro models` y tras un `restart`). La key nunca se
escribe en disco.

**Gateway caído (`turn.step`, antes de `next`).** `GET /health` local con
timeout corto. Si falla: ejecuta `kiro-gateway.ps1 start`, toast
«gateway relanzado» y sigue. Si `start` falla, toast con la ruta del log de
errores y se deja que la petición falle con el error normal de Claude Code.
Un único intento de relanzar por paso.

**Refusal (`turn.step`, después de `next`).** Los chunks pasan en vivo. Si el
paso termina con `stopReason` refusal, el modelo tiene `refusal_fallback` y el
paso aún no se reintentó: repite el paso con `next({ ...e, model: fallback })`
una sola vez y avisa con un toast («sonnet-5.5 cortó → reintento con
sonnet-5»).

- Verificación previa obligatoria (primera tarea del bloque del mod): si Claude
  Code tiene un respaldo nativo para refusals configurable por entorno o
  settings, se usa ese (configurado por `kclaude`) y este hook no se escribe.
- Riesgo: lo que el intento cortado ya mostró queda en el transcript. Si el
  test con el motor muestra que el segundo stream deja la respuesta mal formada,
  se aplica el plan B: no reintentar solo; el toast sugiere `/kiro retry`, que
  cambia al modelo de respaldo y reenvía el último prompt con `$.prompt.submit`.

**`/kiro`.**

| Uso | Hace |
| --- | --- |
| `/kiro` | Estado: versión, uptime, cuenta activa, debug, modelo de la sesión |
| `/kiro restart` | `kiro-gateway.ps1 restart` conservando el `DEBUG_MODE` actual; refresca el estado |
| `/kiro logs [n]` | Últimas `n` líneas (20 por defecto) de `gateway.log` y `gateway.err.log` |
| `/kiro debug all\|errors\|off` | `restart -DebugMode …`; muestra la carpeta de debug |
| `/kiro models` | Tabla: id, thinking nativo, effort, respaldo |
| `/kiro models <id>` | Cambia el modelo de la sesión (equivalente a `/model <id>`) |

Errores de `/kiro`: gateway apagado → lo dice y sugiere `/kiro restart`; 401 →
la key de `kclaude` no coincide con `.env`.

## Cuidado de la cuenta

La única petición extra a Kiro es el reintento por refusal: como mucho una por
paso, solo tras un refusal y solo al modelo que Kiro declara como respaldo. Las
comprobaciones de salud y `/kiro/status` no salen de la máquina
(`/kiro/status` lee lo que el discovery ya tiene).

## Pruebas

- Go (TDD): parser del respaldo en el discovery con un fixture real;
  `/kiro/status` (auth, forma, debug.dir absoluto).
- Mod (`claude plugin test`): inerte sin gateway local; relanzar cuando
  `/health` falla; reintento con respaldo tras refusal y no más de uno; sin
  reintento si no hay respaldo; salida de cada subcomando de `/kiro`.
- `claude plugin validate claude-mod` y `tsc -p claude-mod` en verde.
- Script: prueba manual de start/stop/restart y del cambio de `DEBUG_MODE`.
- En vivo: `kclaude`, `/kiro`, `/kiro restart`, matar el gateway a mano y
  comprobar que el siguiente prompt lo relanza. No se fuerza un refusal real.

## Fuera de alcance

Barra de estado, cambiar el comando `claude` o settings globales, cuota/uso de
Kiro, tokenLimits del discovery.
