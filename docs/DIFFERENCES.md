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
