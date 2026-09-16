# kiro-gateway-go

Port a Go de [kiro-gateway](https://github.com/jwadow/kiro-gateway) como binario único.

Un proxy local que expone las APIs de OpenAI y Anthropic y traduce las peticiones a la API de
Kiro, de forma que cualquier cliente compatible con esas APIs pueda usar los modelos de Kiro.

> **Estado:** Fase 4 (transporte) completada. Multiacceso con descubrimiento en tres formatos,
> token de refresco coalesced singleflight, failover con circuit breaker y sticky selection.
> El payload generado en Go es byte a byte compatible con el original para 1 335 casos de
> corpus (21 descartados con prueba de defecto en grabador). Frontera 1 de D1 cerrada.
> Próxima fase: streaming + rutas (Fase 5). Ver `docs/superpowers/plans/` para más detalles
> del port.

## Licencia

AGPL-3.0. Este proyecto es obra derivada de `jwadow/kiro-gateway`; ver [NOTICE](NOTICE) para la
atribución y la lista de cambios.

## Documentación

- [Diseño del port](docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md)
- [El corpus golden: generación, formato y garantías](docs/CORPUS.md)
- [Correspondencia de módulos](docs/MAPPING.md)
- [Diferencias con el original](docs/DIFFERENCES.md)
