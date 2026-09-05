"""Funciones y clases del upstream que se graban como corpus golden.

FUNCTIONS: funciones puras. Se graba (args, kwargs) -> resultado.
SEQUENCES: clases con estado. Se graba la secuencia completa de llamadas por instancia.
GENERATORS: generadores asincronos de streaming. Se graba (eventos consumidos, args
            escalares) -> fragmentos emitidos.
CONFIG_INPUTS: banderas de configuracion que cada modulo lee sin recibirlas por
            argumento. Se graban DENTRO de la entrada del caso. Ver abajo.
"""

FUNCTIONS = [
    ("kiro.converters_core", "extract_text_content"),
    ("kiro.converters_core", "extract_images_from_content"),
    ("kiro.converters_core", "get_thinking_system_prompt_addition"),
    ("kiro.converters_core", "get_truncation_recovery_system_addition"),
    ("kiro.converters_core", "inject_thinking_tags"),
    ("kiro.converters_core", "sanitize_json_schema"),
    ("kiro.converters_core", "process_tools_with_long_descriptions"),
    ("kiro.converters_core", "validate_tool_names"),
    ("kiro.converters_core", "convert_tools_to_kiro_format"),
    ("kiro.converters_core", "convert_images_to_kiro_format"),
    ("kiro.converters_core", "convert_tool_results_to_kiro_format"),
    ("kiro.converters_core", "extract_tool_results_from_content"),
    ("kiro.converters_core", "extract_tool_uses_from_message"),
    ("kiro.converters_core", "tool_calls_to_text"),
    ("kiro.converters_core", "tool_results_to_text"),
    ("kiro.converters_core", "strip_all_tool_content"),
    ("kiro.converters_core", "ensure_assistant_before_tool_results"),
    ("kiro.converters_core", "merge_adjacent_messages"),
    ("kiro.converters_core", "ensure_first_message_is_user"),
    ("kiro.converters_core", "normalize_message_roles"),
    ("kiro.converters_core", "ensure_alternating_roles"),
    ("kiro.converters_core", "build_kiro_history"),
    ("kiro.converters_core", "build_kiro_payload"),
    ("kiro.converters_openai", "convert_openai_messages_to_unified"),
    ("kiro.converters_openai", "convert_openai_tools_to_unified"),
    ("kiro.converters_openai", "reasoning_effort_to_budget"),
    ("kiro.converters_openai", "extract_thinking_config_from_openai"),
    ("kiro.converters_openai", "build_kiro_payload"),
    ("kiro.converters_anthropic", "convert_anthropic_content_to_text"),
    ("kiro.converters_anthropic", "extract_system_prompt"),
    ("kiro.converters_anthropic", "extract_tool_results_from_anthropic_content"),
    ("kiro.converters_anthropic", "extract_images_from_tool_results"),
    ("kiro.converters_anthropic", "extract_tool_uses_from_anthropic_content"),
    ("kiro.converters_anthropic", "convert_anthropic_messages"),
    ("kiro.converters_anthropic", "convert_anthropic_tools"),
    ("kiro.converters_anthropic", "extract_thinking_config_from_anthropic"),
    ("kiro.converters_anthropic", "anthropic_to_kiro"),
    ("kiro.parsers", "find_matching_brace"),
    ("kiro.parsers", "parse_bracket_tool_calls"),
    ("kiro.parsers", "deduplicate_tool_calls"),
    ("kiro.streaming_anthropic", "format_sse_event"),
    ("kiro.tokenizer", "count_tokens"),
    ("kiro.tokenizer", "count_message_tokens"),
    ("kiro.tokenizer", "count_tools_tokens"),
    ("kiro.tokenizer", "count_system_tokens"),
    ("kiro.tokenizer", "estimate_request_tokens"),
    ("kiro.model_resolver", "to_runtime_model_id"),
    ("kiro.model_resolver", "normalize_model_name"),
    ("kiro.model_resolver", "extract_model_family"),
    ("kiro.payload_guards", "check_payload_size"),
    ("kiro.payload_guards", "trim_payload_to_limit"),
    ("kiro.kiro_errors", "enhance_kiro_error"),
    ("kiro.network_errors", "classify_network_error"),
    ("kiro.network_errors", "format_error_for_user"),
    ("kiro.network_errors", "get_short_error_message"),
    ("kiro.account_errors", "classify_error"),
    ("kiro.truncation_recovery", "should_inject_recovery"),
    ("kiro.truncation_recovery", "generate_truncation_tool_result"),
    ("kiro.truncation_recovery", "generate_truncation_user_message"),
    # generate_conversation_id NO es un generador aleatorio: es un sha256 estable
    # del historial de mensajes, y solo cae a uuid4 cuando no hay mensajes. Su
    # algoritmo es logica portable que necesita cobertura golden, asi que se graba
    # entera y lo unico que se sustituye es uuid.uuid4 en kiro.utils, para que la
    # rama sin mensajes tambien sea determinista.
    ("kiro.utils", "generate_conversation_id"),
]

SEQUENCES = [
    ("kiro.parsers", "AwsEventStreamParser", ["feed", "get_tool_calls", "reset"]),
    ("kiro.thinking_parser", "ThinkingParser", ["feed", "finalize", "reset", "process_for_output"]),
]

# Para cada generador: los nombres de los argumentos escalares que se graban. Los
# argumentos que son objetos (client, response, model_cache, auth_manager) no se
# graban; su presencia queda anotada en el campo notes del caso.
GENERATORS = [
    (
        "kiro.streaming_openai",
        "stream_kiro_to_openai_internal",
        ["model", "first_token_timeout", "request_messages", "request_tools", "conversation_id"],
    ),
    (
        "kiro.streaming_anthropic",
        "stream_kiro_to_anthropic",
        ["model", "first_token_timeout", "request_messages", "request_tools", "request_system", "conversation_id"],
    ),
]

# ==============================================================================
# BANDERAS DE CONFIGURACION QUE ENTRAN EN LA ENTRADA DEL CASO
# ==============================================================================
#
# Varias funciones del upstream NO son puras: leen banderas de kiro.config que no
# llegan por argumento, y los tests las parchean. Grabando solo (args, kwargs) la
# entrada queda INCOMPLETA, y dos llamadas con los mismos argumentos y banderas
# distintas producen salidas distintas con la misma entrada grabada. Eso es lo que
# hacia falsa la implicacion del spec §8.5 ("si todo caso pasa, hay paridad").
#
# Regla: para cada modulo se declaran las banderas de kiro.config que ese modulo
# puede leer, y el grabador las mete en input.config (en las secuencias, en el
# paso __init__). Asi la entrada vuelve a determinar la salida.
#
# La declaracion es POR MODULO, no por funcion, a proposito: la lista se obtiene
# mirando lo que el modulo importa de kiro.config, asi que es completa por
# construccion para lo que sus funciones pueden leer, sin tener que rastrear a mano
# cada rama. El precio es que una funcion que no lee ninguna bandera carga con las
# de su modulo; eso puede duplicar algun caso (mismos argumentos, banderas
# distintas, misma salida), pero nunca puede ocultar una contradiccion, que es lo
# que importa.
#
# A las banderas propias del modulo se suman las de los modulos a los que DELEGA
# entre los que tambien se graban, porque leerlas dentro de la funcion llamada es
# leerlas igual. Por eso converters_openai y converters_anthropic cargan con las de
# converters_core (sus build_kiro_payload llaman al de converters_core) y los dos
# modulos de streaming con las de thinking_parser (construyen un ThinkingParser).
# El cierre se para en lo que puede influir en una salida grabada: no entran las
# banderas de credenciales, rutas, TTL ni logs, que llegan por objetos mockeados
# (client, model_cache, auth_manager) y no por configuracion. Si alguna vez esa
# frontera se pone mal, el detector de conflictos de recorder.py lo dice: una
# bandera que influye y no esta declarada acaba produciendo un conflicto
# "behavioural", y validate.py falla.
#
# Cada bandera se lee del propio modulo objetivo si la tiene como global (es lo
# que ve la funcion, y lo que parchean los tests con
# patch('kiro.converters_core.FAKE_REASONING_ENABLED', ...)), y si no, de
# kiro.config (caso de los `from kiro.config import X` dentro del cuerpo de una
# funcion, como should_inject_recovery).
#
# Los modulos que no aparecen aqui no leen ninguna bandera que pueda variar entre
# llamadas: tokenizer, payload_guards, kiro_errors, network_errors, account_errors,
# utils y model_resolver. El caso de model_resolver merece una nota: importa
# FALLBACK_MODELS, pero solo una vez al importarse, para derivar la constante
# VALID_RUNTIME_MODEL_IDS; parchear la bandera despues no cambia nada, asi que
# grabarla solo anadiria bytes.
_CORE_FLAGS = (
    "TOOL_DESCRIPTION_MAX_LENGTH",
    "FAKE_REASONING_ENABLED",
    "FAKE_REASONING_MAX_TOKENS",
    "FAKE_REASONING_BUDGET_CAP",
    "KIRO_MAX_PAYLOAD_BYTES",
    "AUTO_TRIM_PAYLOAD",
    "TRUNCATION_RECOVERY",
)

_THINKING_PARSER_FLAGS = (
    "FAKE_REASONING_HANDLING",
    "FAKE_REASONING_OPEN_TAGS",
    "FAKE_REASONING_INITIAL_BUFFER_SIZE",
)

_STREAMING_FLAGS = (
    "FIRST_TOKEN_TIMEOUT",
    "FIRST_TOKEN_MAX_RETRIES",
    "TRUNCATION_RECOVERY",
) + _THINKING_PARSER_FLAGS

CONFIG_INPUTS = {
    "kiro.converters_core": _CORE_FLAGS,
    "kiro.converters_openai": ("HIDDEN_MODELS",) + _CORE_FLAGS,
    "kiro.converters_anthropic": ("HIDDEN_MODELS",) + _CORE_FLAGS,
    "kiro.parsers": ("TRUNCATION_RECOVERY",),
    "kiro.truncation_recovery": ("TRUNCATION_RECOVERY",),
    "kiro.thinking_parser": _THINKING_PARSER_FLAGS,
    "kiro.streaming_openai": _STREAMING_FLAGS,
    "kiro.streaming_anthropic": _STREAMING_FLAGS,
}

# Funciones que NO se graban, con el motivo. Sirve para que nadie las añada por error.
EXCLUDED = {
    "kiro.utils:generate_completion_id": "generador de IDs, sustituido por version determinista en grabacion",
    "kiro.utils:generate_tool_call_id": "generador de IDs, sustituido por version determinista en grabacion",
    "kiro.utils:get_kiro_headers": "depende del fingerprint de la máquina y de un uuid",
    "kiro.utils:get_machine_fingerprint": (
        "sha256 del hostname y del usuario de la maquina: su golden solo vale en la maquina "
        "que lo grabo y pondria el CI en rojo aunque el port fuese correcto. Ademas publicaria "
        "un identificador pseudonimo estable del desarrollador en un fichero versionado. "
        "La prueba correcta la define el spec §8.4: comparar Python y Go en la MISMA maquina, "
        "sin pasar por el corpus. Es el mismo motivo por el que get_kiro_headers esta excluida."
    ),
    "kiro.streaming_anthropic:generate_message_id": "generador de IDs, sustituido por version determinista en grabacion",
    "kiro.streaming_anthropic:generate_thinking_signature": "generador de IDs, sustituido por version determinista en grabacion",
    "kiro.model_resolver:get_model_id_for_kiro": "envoltorio de ModelResolver.resolve, que es asincrono y depende de red mockeada",
}
