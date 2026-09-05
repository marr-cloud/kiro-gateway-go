// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package utils

import (
	"context"
	"net/http"
)

// TokenProvider es la interfaz mínima que GetKiroHeaders necesita de la capa
// de autenticación. Se declara aquí, en el CONSUMIDOR, y no en internal/auth,
// para romper el ciclo utils ↔ auth que existe en el original (auth importa
// utils para la huella, utils importa auth para el tipo). auth.Manager (fase
// 2d/3) la satisfará de forma estructural sin conocer este paquete: es
// exactamente el patrón que Go recomienda para dependencias entre paquetes.
type TokenProvider interface {
	AccessToken(ctx context.Context) (string, error)
	ProfileARN() string
}

// GetKiroHeaders devuelve las nueve cabeceras que Kiro exige en cada
// petición, byte a byte según .upstream/kiro/utils.py :: get_kiro_headers.
//
// Frontera de paridad. El backend de Kiro puede validar estas cabeceras y el
// User-Agent mimetiza el cliente oficial (KiroIDE 0.7.45 sobre aws-sdk-js
// 1.0.27). Los valores fijos no se derivan de configuración: si cambian,
// cambian aquí y en el original a la vez, o el port pierde paridad.
//
// Capitalización. http.Header.Set canonicaliza claves (Set("x-amz-target",
// ...) escribe "X-Amz-Target"). El original manda las claves en minúscula.
// En HTTP/1.1 la comparación es case-insensitive y Go/net/http respeta el
// case del mapa cuando escribe la línea de cabecera, así que en la práctica
// da igual: no es un riesgo de paridad. Aun así, construimos el mapa con un
// literal http.Header{} en vez de Set para que el mapa devuelto sea idéntico
// al del original (lo verifica TestKiroHeadersPreserveCase). Si mañana pasa
// a HTTP/2, hpack forzará minúsculas en el pinchazo y el efecto en el hilo
// será el mismo. Ver el informe de la fase 2a.
func GetKiroHeaders(ctx context.Context, tp TokenProvider) (http.Header, error) {
	token, err := tp.AccessToken(ctx)
	if err != nil {
		return nil, err
	}
	fp := MachineFingerprint()

	return http.Header{
		"Authorization":               {"Bearer " + token},
		"Content-Type":                {"application/x-amz-json-1.0"},
		"x-amz-target":                {"AmazonCodeWhispererStreamingService.GenerateAssistantResponse"},
		"User-Agent":                  {"aws-sdk-js/1.0.27 ua/2.1 os/win32#10.0.19044 lang/js md/nodejs#22.21.1 api/codewhispererstreaming#1.0.27 m/E KiroIDE-0.7.45-" + fp},
		"x-amz-user-agent":            {"aws-sdk-js/1.0.27 KiroIDE-0.7.45-" + fp},
		"x-amzn-codewhisperer-optout": {"true"},
		"x-amzn-kiro-agent-mode":      {"vibe"},
		"amz-sdk-invocation-id":       {NewUUID()},
		"amz-sdk-request":             {"attempt=1; max=3"},
	}, nil
}
