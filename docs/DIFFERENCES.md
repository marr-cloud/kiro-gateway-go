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
imagen resultante mide ~15 MB frente a ~150 MB del original.

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

**Por qué:** el port solo lee de `.env` dos rutas de credenciales (`KIRO_CREDS_FILE`,
`KIRO_CLI_DB_FILE`), y en Windows esas rutas contienen backslashes que un intérprete de escapes
convertiría en secuencias no deseadas. Leerlas en crudo es lo correcto para su único consumidor.

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

## Comportamientos del original que se replican a propósito

El upstream tiene cinco comportamientos que son defectos o atajos, pero **se replican a propósito
byte por byte** porque cambiarlos alteraría lo que ven los clientes o rompería compatibilidad con
el backend de Kiro. Replicar un defecto del sistema al que se adapta un cliente es correcto:
permite que este port sea un reemplazo directo sin sorpresas. Corregirlos sería divergir.

| Comportamiento | Descripción | Por qué se replica |
|---|---|---|
| **Parser oportunista del stream** | `parsers.py` no decodifica el framing binario de AWS event-stream: descarta bytes inválidos y rescata JSON buscando siete prefijos literales (`{"content":`, `{"name":`, `{"input":`, `{"stop":`, `{"followupPrompt":`, `{"usage":`, `{"contextUsagePercentage":`). No hay decoder real del protocolo ni siquiera detrás de un flag | Este atajo es el corazón del gateway. El stream de Kiro llega en un formato binario de AWS que el original nunca parseó correctamente, y funciona porque encuentra el JSON incrustado. Cambiarlo a un parser real corre el riesgo de divergir en casos borde que el parser oportunista ya maneja, y no hay forma de saber cuáles son sin romper cosas en producción |
| **Decodificación UTF-8 por chunk independiente** | Cada chunk del stream se decodifica de forma aislada con `chunk.decode('utf-8', errors='ignore')`. Si un carácter multibyte queda partido entre dos chunks, los bytes parciales se descartan y el carácter se corrompe. El port usa `bytes.ToValidUTF8(chunk, nil)` sin reensamblar runas | Es un defecto del original, pero cambiar el comportamiento alteraría los offsets en el buffer del parser, y con ello la secuencia de eventos emitidos. Los clientes nunca notaron el problema, lo que sugiere que Kiro no parte caracteres multibyte en la práctica. Arreglarlo es riesgo sin beneficio demostrado |
| **Factor de corrección 1.15 del tokenizer** | El port usa el encoding BPE `cl100k_base` (de GPT-4) y multiplica el resultado por 1.15 para aproximar la tokenización real de Claude. La corrección se aplica con truncamiento hacia cero: `int(total * 1.15)` | El conteo de tokens alimenta el campo `usage` que ven los clientes. Cambiarlo rompería cualquier código que confíe en esos números. El factor 1.15 es una heurística del original; sin acceso al tokenizer real de Claude, es lo mejor disponible |
| **`Connection: close` en streams** | El gateway añade la cabecera `Connection: close` en las peticiones de streaming hacia Kiro | Mitigación de fugas de sockets CLOSE_WAIT observadas en el despliegue del original. Quitarla podría reintroducir el problema |
| **HTTP/1.1 forzado** | El cliente HTTP del gateway desactiva HTTP/2 explícitamente, incluso para HTTPS | El original usa `httpx`, que no activa HTTP/2 por defecto. Go sí lo activa en HTTPS si el servidor lo anuncia. El backend de Kiro espera HTTP/1.1, y enviarle HTTP/2 podría cambiar comportamientos sutiles del protocolo o del balanceador. Se fuerza 1.1 para que el fingerprint del gateway sea idéntico al original |

Ninguno de estos cinco comportamientos se debe arreglar sin coordinación con el upstream y prueba
en entorno real con Kiro. La razón de ser de este port es la **paridad**, no la corrección. Si el
original funciona con estos atajos, el port también.
