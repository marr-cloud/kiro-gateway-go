# kiro-gateway-go

Port a Go de [kiro-gateway](https://github.com/jwadow/kiro-gateway) como binario único.

Un proxy local que expone las APIs de OpenAI y Anthropic y traduce las peticiones a la API de
Kiro, de forma que cualquier cliente compatible con esas APIs pueda usar los modelos de Kiro.

> **Estado:** Fase 5 (streaming y rutas) completada. Primer binario funcional:
> `go build -o kiro-gateway ./cmd/kiro-gateway/` produce un ejecutable que sirve `/`, `/health`
> y los cuatro endpoints de la API (`/v1/models`, `/v1/chat/completions`, `/v1/messages`,
> `/v1/messages/count_tokens`) detrás de CORS, auth por dialecto y recuperación de panics.
> Pipeline de streaming (parsers, thinking parser, truncation, `streamingcore` + los dos
> formatters SSE) y las dos capas de rutas con failover/circuit breaker completos. Frontera 2 de
> D1 cerrada: los bytes SSE producidos por los formatters coinciden con los goldens del corpus.
> Próxima fase: MCP tools, debug logger/middleware, conformance de doble binario y empaquetado
> (Fase 6). Ver `docs/superpowers/plans/` para más detalles del port.

## Licencia

AGPL-3.0. Este proyecto es obra derivada de `jwadow/kiro-gateway`; ver [NOTICE](NOTICE) para la
atribución y la lista de cambios.

## Documentación

- [Diseño del port](docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md)
- [El corpus golden: generación, formato y garantías](docs/CORPUS.md)
- [Correspondencia de módulos](docs/MAPPING.md)
- [Diferencias con el original](docs/DIFFERENCES.md)
