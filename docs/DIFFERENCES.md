# Diferencias con el original

Este documento lista todos los cambios deliberados respecto al upstream
`jwadow/kiro-gateway` (v2.4.dev.13, commit `a5292ca`), y los comportamientos del original que se
replican a propósito incluso cuando parecen defectos.

## Diferencias funcionales y de empaquetado

### 1. Ausencia de `/docs`, `/redoc` y `/openapi.json`

**Qué cambia:** estos tres endpoints no existen en el port.

**Por qué:** FastAPI los genera automáticamente a partir de las anotaciones de tipo. En Go
habría que escribir y mantener a mano una especificación OpenAPI completa para un proxy que nadie
explora por navegador. Es trabajo real sin beneficio.

**Impacto:** si alguien exploraba el gateway con Swagger UI, ya no puede. Los endpoints
funcionales (`/v1/chat/completions`, `/v1/messages`, etc.) no cambian.

---

### 2. Forma de los errores 422

**Qué cambia:** el port mantiene el envoltorio (`detail` con lista de objetos que contienen
`loc`, `msg` y `type`, más `body` truncado a 500 caracteres), pero no reproduce la forma exacta
de Pydantic v2, que incluye campos como `input` y `url`.

**Por qué:** reproducir literalmente el formato de Pydantic v2 exigiría serializar su estructura
de errores completa, que es trabajo real para algo que ningún cliente consume de forma
programática. Los clientes legítimos leen el `message` y siguen adelante.

**Impacto:** un script que parsee la estructura interna de un 422 verá campos distintos. El
mensaje de error sigue siendo legible y el código de estado es el mismo.

---

### 3. JSON más rico en `GET /` y `GET /health`

**Qué cambia:** el port conserva los campos del original (`status`, `message`, `version` en `/`,
y `status`, `timestamp`, `version` en `/health`) y añade información adicional: cuenta activa,
uptime y modo de operación.

**Por qué:** el usuario autorizó esta mejora explícitamente. Son endpoints de healthcheck y nadie
depende de su forma exacta; añadir contexto operativo es útil.

**Impacto:** un cliente que deserialice la respuesta en un struct rígido podría ignorar los
campos nuevos o fallar si no tolera campos desconocidos. Un cliente que lea solo los campos que
conoce no nota diferencia.

---

### 4. Flag `--health` en el CLI

**Qué cambia:** el port añade un flag `--health` que hace un GET a `/health` del gateway en
ejecución y sale con código 0 si responde exitosamente, o 1 en caso contrario.

**Por qué:** el healthcheck de Docker del original ejecuta `python -m httpx` desde dentro del
contenedor. En una imagen Docker mínima que solo contiene el binario Go no hay Python, ni httpx,
ni curl. Necesitábamos una forma de hacer el healthcheck usando únicamente el propio binario.

**Impacto:** ninguno para quien use el gateway de forma interactiva. Para quien lo despliegue con
Docker, el healthcheck funciona igual pero el comando cambia de `python -m httpx ...` a
`kiro-gateway --health`.

---

### 5. Imagen Docker mínima

**Qué cambia:** el Dockerfile del port produce una imagen mínima con el binario estático y nada
más, en vez de partir de `python:3.10-slim`.

**Por qué:** es el objetivo central del port: un único binario autocontenido sin intérprete. La
imagen resultante mide ~27 MB frente a ~150 MB del original.

**Impacto:** cualquier flujo que dependiera de tener Python o herramientas del sistema
disponibles dentro del contenedor (ejecutar scripts auxiliares, usar `pip`, etc.) deja de
funcionar. Para quien solo necesite el gateway, la imagen es más liviana y arranca más rápido.

---

### 6. Documentación en dos idiomas en vez de siete

**Qué cambia:** el README se ofrece solo en inglés y español, no en los siete idiomas del
original (inglés, español, francés, alemán, italiano, portugués, chino simplificado).

**Por qué:** mantener siete traducciones sincronizadas es carga real de mantenimiento. Dos
idiomas cubren la mayoría de los usuarios y permiten iterar sin bloqueo.

**Impacto:** quien buscaba documentación en francés, alemán, italiano, portugués o chino no la
encuentra. El README en inglés sigue siendo exhaustivo.

---

### 7. Serialización de floats con magnitud grande en `pyjson`

**Qué cambia:** `internal/pyjson/dumps.go` (y `str.go`) formatea los `float64` con `strconv` en
formato `'g'` shortest. Para magnitudes grandes (aproximadamente `|v| >= 1e6`) Go conmuta a
notación exponencial, mientras que `json.dumps` de Python conserva el decimal: `1000000.0` sale
como `"1e+06"` en el port y como `"1000000.0"` en el original. Los floats con valor entero
(`1.0`) y los pequeños coinciden byte por byte.

**Por qué:** `strconv.FormatFloat` con precisión `-1` es el shortest round-trip de la stdlib de
Go, y no hay API estándar para forzar el formato decimal que usa CPython sin reimplementar
Grisu/Ryu. Escribir un formateador propio para un caso que hoy no tiene consumidor era coste sin
beneficio.

**Impacto:** ningún paquete de fase 2a lo consume. El primer usuario será el tokenizador de fase
2b, que se valida contra el corpus golden: cualquier divergencia observable saltaría allí y se
trataría entonces.

---

### 8. Escapes estilo python-dotenv en `.env`

**Qué cambia:** `internal/config/dotenv.go` no interpreta las secuencias de escape (`\n`, `\t`,
`\"`, `\\`, ...) dentro de valores entrecomillados que sí procesa `python-dotenv`. El port
devuelve la cadena tal cual para todas las variables.

**Por qué:** los valores de `.env` que importan aquí son rutas del sistema de ficheros
(`ACCOUNTS_CONFIG_FILE` y, para el camino de cuenta única de `internal/auth`, `KIRO_CREDS_FILE` /
`KIRO_CLI_DB_FILE` — ver §10); en Windows contienen backslashes que un intérprete de escapes
convertiría en secuencias no deseadas. Leerlas en crudo es lo correcto para esos consumidores, y
ninguna otra variable del port depende de expandir escapes.

**Impacto:** no observable en el corpus: el grabador aborta si detecta un `.env` en el camino de
búsqueda. Un usuario que pusiera escapes intencionados en su `.env` los vería literales; se
documenta para que fase 2b/3 no los reintroduzca por reflejo si extiende el parser.

---

### 9. Rutas de credenciales sin `filepath.Clean`

**Qué cambia:** `internal/config/config.go` no pasa `KIRO_CREDS_FILE` ni `KIRO_CLI_DB_FILE` por
`filepath.Clean`, a diferencia del original, que normaliza separadores al construir
`str(Path(...))` en Windows.

**Por qué:** `os.Open` acepta indistintamente `/` y `\` en Windows, la ruta se usa una sola vez
para abrir el fichero, y `filepath.Clean` puede colapsar barras de forma que rompa prefijos UNC
o rutas verbatim (`\\?\...`). Preservar el literal es más seguro que canonicalizarlo.

**Impacto:** equivalente para `os.Open`. Si alguien loguea la ruta efectiva verá exactamente el
valor que puso en el `.env`, no una versión saneada.

---

### 10. Modelo de credenciales: solo `credentials.json`

**Qué cambia:** el port carga las cuentas únicamente desde un array `credentials.json` (nombrado por
`ACCOUNTS_CONFIG_FILE`, por defecto `credentials.json` en el directorio de trabajo). No implementa el
modo de "cuenta única" del original —`REFRESH_TOKEN`, `KIRO_CREDS_FILE` o `KIRO_CLI_DB_FILE` sueltos
en el `.env` como fuente de cuenta— ni el flag `ACCOUNT_SYSTEM` (que en el original activa el sistema
multi-cuenta y aquí no gatea nada: el sistema de cuentas está siempre activo). `internal/auth` sí sabe
cargar una credencial JSON o SQLite individual (lo usa `discovery` por cada entrada del array, y sus
tests directamente), por lo que `KIRO_CREDS_FILE`/`KIRO_CLI_DB_FILE` siguen siendo variables válidas
de `config` (ver §8, §9), pero el gateway en ejecución no las usa como fuente de cuentas.

**Por qué:** `main.py` del original tiene dos rutas de credenciales (legacy de una cuenta y el sistema
de cuentas). Portar ambas duplicaba la lógica de arranque para un beneficio marginal; el sistema de
cuentas cubre el caso de una sola cuenta (un array de un elemento) sin bifurcación. Menos superficie,
un solo camino de descubrimiento.

**Impacto:** un `.env` del original que configure la credencial con `REFRESH_TOKEN`/`KIRO_CREDS_FILE`/
`KIRO_CLI_DB_FILE` no carga ninguna cuenta en el port (devuelve 503 "no accounts available"). La
migración es crear un `credentials.json` con una entrada equivalente. Ver
[`credentials.json.example`](../credentials.json.example) y la sección Configuración del README.

---

### 11. Lista de modelos configurable (`models.json`)

**Qué cambia:** el port añade un fichero opcional `models.json` (nombrado por `MODELS_CONFIG_FILE`, por
defecto `models.json` en el directorio de trabajo): un array JSON de IDs de modelo que, si existe, es la
lista autoritativa que devuelve `GET /v1/models` (corto-circuita tanto el descubrimiento dinámico como
la lista estática `fallbackModels`). Si el fichero no existe, el comportamiento es el de siempre (lista
estática); si existe pero está malformado, el arranque falla con un error claro.

**Por qué:** el endpoint runtime de Kiro (`runtime.*.kiro.dev`) no expone `ListAvailableModels` ("AWS
limitation", replicado del upstream), así que la lista estática se queda obsoleta cuando Kiro publica
modelos nuevos (p. ej. `claude-sonnet-5`, `claude-opus-4.8`, `gpt-5.6-*`). El upstream no ofrece forma
de actualizarla sin recompilar. `models.json` deja al operador mantener la lista al día sin tocar el
binario, sin acoplarse a un API que Kiro no expone.

**Impacto:** ninguno por defecto (sin fichero, la lista es la misma que antes). El resolver del port ya
es *passthrough* ("gateway, not gatekeeper"): un modelo no listado se pasa igualmente a Kiro, así que
`models.json` solo afecta al **listado**, nunca a qué modelos se pueden **usar**. No es un límite de
wire de conversaciones: no toca la paridad byte a byte.

---

### 12. Descubrimiento dinámico de modelos (`management.<region>.kiro.dev`)

**Qué cambia:** para las cuentas de endpoint runtime, el port descubre la lista de modelos
dinámicamente llamando a `ListAvailableModels` contra `management.<region>.kiro.dev` (protocolo AWS
JSON 1.0: `POST` con `X-Amz-Target: AmazonCodeWhispererService.ListAvailableModels`, `origin=KIRO_CLI`
y el bearer token de la cuenta), en vez de servir siempre la lista estática. Es exactamente lo que hace
el Kiro CLI actual. Si la llamada falla (auth, red, status≠200, parseo), cae a la lista estática; y un
`models.json` (§11), si existe, tiene prioridad sobre ambos. Orden: **`models.json` > dinámico >
estática**.

**Por qué:** el upstream (fijado en `a5292ca`) y el antiguo `amazon-q-developer-cli` solo conocían
`q.*.amazonaws.com/ListAvailableModels`, que el endpoint runtime NO expone ("AWS limitation"). Pero el
Kiro CLI actual movió esa operación a un plano de control nuevo, `management.*.kiro.dev`, que el
upstream desconoce. Consultarlo deja que `/v1/models` refleje los modelos reales de la cuenta (p. ej.
`claude-opus-5`, `claude-sonnet-5`, `gpt-5.6-*`) sin mantenimiento manual. Se construye la petición a
mano (no vía `httpclient.RequestWithRetry`, que fuerza el `X-Amz-Target` de `GenerateAssistantResponse`).

**Impacto:** `/v1/models` pasa a reflejar la cuenta en vez de una lista fija; ninguno si el endpoint no
responde (fallback). No es un límite de wire de conversaciones: no toca la paridad byte a byte. La
respuesta también trae `tokenLimits{maxInputTokens,...}` (hoy sin consumir; ver el TODO de `200000`).

---

Las secciones 13 a 15 adaptan el gateway a Claude Code usado como cliente (`ANTHROPIC_BASE_URL`; ver
`scripts/kiro-claude.ps1`). Siguen el contrato de
[code.claude.com/docs/en/llm-gateway-protocol](https://code.claude.com/docs/en/llm-gateway-protocol).

### 13. Contexto excedido con la forma de error de Anthropic

**Qué cambia:** en `/v1/messages`, cuando Kiro rechaza la petición con
`reason: CONTENT_LENGTH_EXCEEDS_THRESHOLD`, el gateway responde `400` con `type: invalid_request_error`
y el mensaje `prompt is too long: Model context limit reached. Conversation size exceeds model
capacity. (capability_rejected: prompt_too_long)`. El original responde con el status de Kiro,
`type: api_error` y solo el mensaje de `kiroerrors`. `/v1/chat/completions` no cambia.

**Por qué:** Claude Code solo compacta la conversación de forma reactiva cuando reconoce un rechazo por
prompt demasiado largo: el texto de Anthropic `prompt is too long` o el token estable
`capability_rejected: prompt_too_long`. Con el error original, la sesión se queda atascada en el
límite de contexto.

**Impacto:** cambia el cuerpo de error de un único caso Fatal. `kiroerrors.Enhance` y su corpus golden
no cambian; el resto de errores Fatal conservan la forma del original.

---

### 14. Eventos `ping` durante el streaming Anthropic

**Qué cambia:** en `/v1/messages` con `stream: true`, después de `message_start`, el gateway emite
`event: ping` / `data: {"type": "ping"}` cada 15 s hasta que el stream termina. El original define el
evento pero nunca lo emite.

**Por qué:** Claude Code aborta un stream cuando no recibe bytes durante su idle timeout (300 s por
defecto), y Kiro puede callar más tiempo en un thinking largo. Los pings son los únicos bytes que
mantienen viva la conexión en esa pausa.

**Impacto:** hay eventos `ping` adicionales entre los demás eventos del stream; los clientes Anthropic
los ignoran por contrato. La respuesta no-streaming no cambia.

---

### 15. `display_name` en `/v1/models`

**Qué cambia:** cada entrada de `/v1/models` lleva `display_name`, derivado del id
(`claude-sonnet-4.5` → `Claude Sonnet 4.5`, `gpt-5.6-luna` → `GPT 5.6 Luna`).

**Por qué:** Claude Code no reconoce los ids con punto de Kiro y, sin `display_name`, los muestra crudos
en el picker `/model` cuando el discovery de modelos del gateway está activo.

**Impacto:** campo aditivo; los clientes OpenAI ignoran campos desconocidos.

---

### 16. Fake reasoning solo en una lista de modelos (`FAKE_REASONING_MODELS`)

**Qué cambia:** la inyección de fake reasoning (la adición "Extended Thinking Mode" al system prompt y
las etiquetas `<thinking_mode>`/`<max_thinking_length>`/`<thinking_instruction>` en el mensaje actual)
solo se aplica a los modelos de `FAKE_REASONING_MODELS`. Por defecto es la lista verificada en vivo:
`claude-haiku-4.5`, `claude-sonnet-4`, `claude-sonnet-4.5`, `claude-sonnet-4.6`, `claude-sonnet-5`,
`claude-opus-4.5`, `claude-opus-4.6`, `claude-opus-4.7` y `qwen3-coder-next`. `*` restaura el
comportamiento del original (todos los modelos). Los ids de la lista se normalizan como los de las
peticiones (`claude-sonnet-4-5` equivale a `claude-sonnet-4.5`). `FAKE_REASONING=false` sigue
desactivándolo para todos.

**Por qué:** `claude-sonnet-5.5` y `claude-opus-5` razonan de forma nativa y su clasificador corta la
respuesta con `stopReason: CONTENT_FILTERED` / `REASONING_EXTRACTION` cuando el prompt les pide volcar
ese razonamiento en el texto (sonnet-5.5: 5 de 6 cortes sin `thinking` y 3 de 4 con `adaptive`, lo que
manda Claude Code). En opus-5 basta la adición al system prompt, que el original añade incluso con
`thinking: disabled`. Sin inyección, los dos responden bien siempre. El resto de modelos fuera de la
lista no saca provecho: unos la ignoran (`claude-opus-5.5`, `deepseek-3.2`, `glm-5`) y otros razonan de
forma nativa sin mostrarlo (`claude-opus-4.8`, `gpt-5.6-*`, `minimax-*`), así que pedirles que vuelquen
su razonamiento solo añade riesgo de rechazos para la cuenta.

**Impacto:** los modelos fuera de la lista no devuelven bloque thinking. Un modelo nuevo de Kiro
empieza sin inyección hasta que se añada a la lista. Los modelos con razonamiento nativo (§18) nunca
reciben la inyección, estén o no en la lista.

---

### 17. Cortes de Kiro (`CONTENT_FILTERED`) visibles para el cliente

**Qué cambia:** cuando Kiro corta la respuesta con un `metadataEvent`
`{"stopDetails":{"refusal":{"category":…,"explanation":…}},"stopReason":"CONTENT_FILTERED"}`, el
gateway termina con `stop_reason: "refusal"` en `/v1/messages` y `finish_reason: "content_filter"` en
`/v1/chat/completions`, y registra un aviso con la categoría y la explicación. El parser reconoce dos
prefijos más (`{"stopDetails":` y `{"stopReason":`); solo `CONTENT_FILTERED` produce un evento, así que
un `{"stopReason":"END_TURN"}` se sigue descartando como antes. Un corte nunca se trata como truncado,
así que no dispara la recuperación de truncación.

**Por qué:** el original ignora el `metadataEvent` y devuelve un `end_turn`/`stop` normal con la
respuesta vacía o cortada a la mitad, sin ningún aviso. El cliente no puede distinguir un corte de una
respuesta terminada.

**Impacto:** solo cambia el motivo de parada de las respuestas cortadas; el contenido parcial se
entrega igual que antes.

---

### 18. Razonamiento nativo oficial de Kiro (`additionalModelRequestFields`)

**Qué cambia:** el gateway usa el mismo mecanismo que el IDE de Kiro para el thinking real del modelo:

- **Capacidades:** el discovery (§12) lee el `additionalModelRequestFieldsSchema` de cada modelo en
  `ListAvailableModels`: qué valores de `thinking.type` acepta (`adaptive`, `disabled`,
  `between_tools`), `thinking.display`, y el nivel de esfuerzo en `output_config.effort` (Claude) o
  `reasoning.effort` (GPT).
- **Petición:** en esos modelos, `thinking` y `output_config.effort` de `/v1/messages` (lo que manda
  Claude Code) se envían a Kiro en `additionalModelRequestFields`, en el nivel superior de
  `GenerateAssistantResponse`. `enabled` con `budget_tokens` se convierte en `adaptive`; un valor que el
  esquema del modelo no admite se omite. Con el thinking desactivado, `xhigh`/`max` bajan al nivel más
  alto restante, como hace el IDE. En `/v1/chat/completions`, `reasoning_effort` activa `adaptive` con
  ese esfuerzo y `none` lo desactiva. Estos modelos nunca reciben fake reasoning (§16).
- **Respuesta:** los `reasoningContentEvent` (`{"text":…}`, `{"signature":…}`, `{"redactedContent":…}`)
  salen como bloques `thinking` con la firma real en un `signature_delta`, o `redacted_thinking`, en
  `/v1/messages`, y como `reasoning_content` en `/v1/chat/completions` (sin firma ni contenido cifrado,
  que OpenAI no modela). Sin texto de razonamiento (modelos que lo ocultan) se emite un bloque `thinking`
  vacío con su firma. `FAKE_REASONING_HANDLING` se aplica igual que al fake reasoning.
- **Historial:** los bloques `thinking` firmados y `redacted_thinking` de los mensajes del asistente del
  turno actual (desde el último mensaje del usuario sin `tool_result`) vuelven a Kiro en
  `assistantResponseMessage.reasoningContent`, como hace el IDE. Las firmas `sig_…` del fake reasoning
  y los turnos anteriores no se envían: Kiro valida la firma y rechaza una que no reconoce
  (`THINKING_SIGNATURE_INVALID`), y un turno anterior puede venir de otro modelo.

**Por qué:** el fake reasoning pide al modelo que escriba su razonamiento en el texto, algo que los
modelos con razonamiento nativo rechazan o ignoran (§16). El mecanismo oficial devuelve el razonamiento
real, sin riesgo de cortes, y hace que el esfuerzo que elige el cliente (`/effort` en Claude Code)
llegue al modelo.

**Impacto:** los modelos con capacidades nativas devuelven bloques thinking reales en lugar de nada.
Si el discovery no está disponible (lista estática o `models.json`), no hay capacidades registradas y
todo funciona como antes, con el fake reasoning de §16.

---

### 19. `DEBUG_MODE` registra la respuesta completa en ambas rutas

**Qué cambia:** el port tenía portado el `DebugLogger`, pero las rutas solo llamaban a
`LogRequestBody` (desde el middleware). Ahora se cablea como en el original: el payload a Kiro
(`kiro_request_body.json`), cada chunk crudo del stream (`response_stream_raw.txt`), el volcado
en errores (`FlushOnError`: error Fatal de Kiro, fallo al construir el payload o error a mitad de
stream) y el descarte en las respuestas correctas (`DiscardBuffers`). Sin esto, `DEBUG_MODE=errors`
no escribía nunca nada.

La divergencia está en `response_stream_modified.txt`: el original solo registra los deltas de texto
de la ruta OpenAI (`streaming_openai.py:154,183,252`). El port registra **todo** lo que sale hacia el
cliente, en ambas rutas: los eventos SSE en streaming y el JSON final en no-streaming.

**Impacto:** con `DEBUG_MODE=all` se puede comparar lo que mandó Kiro con lo que recibió el cliente
(Claude Code incluido), algo que hacía falta para diagnosticar cortes como los de §17. Con
`DEBUG_MODE=off` no cambia nada: los envoltorios no se instalan.

---

### 20. `GET /kiro/status` y el modelo de respaldo por refusal

**Qué cambia:** endpoint nuevo, sin equivalente en el original, protegido con `PROXY_API_KEY`
(`Authorization: Bearer` o `x-api-key`). Devuelve la versión, el uptime, la cuenta activa, el
`DEBUG_MODE` con su carpeta absoluta y, por modelo, el razonamiento nativo (§18) y el modelo de
respaldo por refusal. Ese respaldo sale de `refusalFallbackModels`, un campo de
`ListAvailableModels` que el original no lee (`claude-sonnet-5.5 → claude-sonnet-5`,
`claude-opus-5.5` y `claude-opus-5 → claude-opus-4.8`).

**Impacto:** lo usa el mod de Claude Code (`claude-mod/`) para `/kiro` y para reintentar un corte
(§17) con el modelo que declara Kiro. No hace peticiones a Kiro: lee lo que guardó el discovery.
Las rutas existentes no cambian.

---

## Comportamientos del original que se replican a propósito

El upstream tiene cinco comportamientos que son defectos o atajos, pero **se replican a propósito
byte por byte** porque cambiarlos alteraría lo que ven los clientes o rompería compatibilidad con
el backend de Kiro. Replicar un defecto del sistema al que se adapta un cliente es correcto:
permite que este port sea un reemplazo directo sin sorpresas. Corregirlos sería divergir.

| Comportamiento | Descripción | Por qué se replica |
|---|---|---|
| **Parser oportunista del stream** | `parsers.py` no decodifica el framing binario de AWS event-stream: descarta bytes inválidos y rescata JSON buscando siete prefijos literales (`{"content":`, `{"name":`, `{"input":`, `{"stop":`, `{"followupPrompt":`, `{"usage":`, `{"contextUsagePercentage":`). No hay decoder real del protocolo ni siquiera detrás de un flag. El port solo añade los prefijos de `metadataEvent` (§17) | Este atajo es el corazón del gateway. El stream de Kiro llega en un formato binario de AWS que el original nunca parseó correctamente, y funciona porque encuentra el JSON incrustado. Cambiarlo a un parser real corre el riesgo de divergir en casos borde que el parser oportunista ya maneja, y no hay forma de saber cuáles son sin romper cosas en producción |
| **Decodificación UTF-8 por chunk independiente** | Cada chunk del stream se decodifica de forma aislada con `chunk.decode('utf-8', errors='ignore')`. Si un carácter multibyte queda partido entre dos chunks, los bytes parciales se descartan y el carácter se corrompe. El port usa `bytes.ToValidUTF8(chunk, nil)` sin reensamblar runas | Es un defecto del original, pero cambiar el comportamiento alteraría los offsets en el buffer del parser, y con ello la secuencia de eventos emitidos. Los clientes nunca notaron el problema, lo que sugiere que Kiro no parte caracteres multibyte en la práctica. Arreglarlo es riesgo sin beneficio demostrado |
| **Factor de corrección 1.15 del tokenizer** | El port usa el encoding BPE `cl100k_base` (de GPT-4) y multiplica el resultado por 1.15 para aproximar la tokenización real de Claude. La corrección se aplica con truncamiento hacia cero: `int(total * 1.15)` | El conteo de tokens alimenta el campo `usage` que ven los clientes. Cambiarlo rompería cualquier código que confíe en esos números. El factor 1.15 es una heurística del original; sin acceso al tokenizer real de Claude, es lo mejor disponible |
| **`Connection: close` en streams** | El gateway añade la cabecera `Connection: close` en las peticiones de streaming hacia Kiro | Mitigación de fugas de sockets CLOSE_WAIT observadas en el despliegue del original. Quitarla podría reintroducir el problema |
| **HTTP/1.1 forzado** | El cliente HTTP del gateway desactiva HTTP/2 explícitamente, incluso para HTTPS | El original usa `httpx`, que no activa HTTP/2 por defecto. Go sí lo activa en HTTPS si el servidor lo anuncia. El backend de Kiro espera HTTP/1.1, y enviarle HTTP/2 podría cambiar comportamientos sutiles del protocolo o del balanceador. Se fuerza 1.1 para que el fingerprint del gateway sea idéntico al original |

Ninguno de estos cinco comportamientos se debe arreglar sin coordinación con el upstream y prueba
en entorno real con Kiro. La razón de ser de este port es la **paridad**, no la corrección. Si el
original funciona con estos atajos, el port también.
