# kiro-gateway-go

Port a Go de [kiro-gateway](https://github.com/jwadow/kiro-gateway) como binario único.

Un proxy local que expone las APIs de OpenAI y Anthropic y traduce las peticiones a la API de
Kiro, de forma que cualquier cliente compatible con esas APIs pueda usar los modelos de Kiro.

> **Estado:** en construcción. El binario todavía no sirve peticiones. Ver
> `docs/superpowers/plans/` para el estado del port.

## Licencia

AGPL-3.0. Este proyecto es obra derivada de `jwadow/kiro-gateway`; ver [NOTICE](NOTICE) para la
atribución y la lista de cambios.

## Documentación

- [Diseño del port](docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md)
- [El corpus golden: generación, formato y garantías](docs/CORPUS.md)
- [Correspondencia de módulos](docs/MAPPING.md)
- [Diferencias con el original](docs/DIFFERENCES.md)
