"""Plugin de pytest que graba casos golden de la suite del upstream.

Uso:
    CORPUS_OUT=/ruta/testdata pytest -p recorder

Se instala como plugin apuntando PYTHONPATH a este directorio.

=============================================================================
EL CORPUS SOLO ES REPRODUCIBLE CON LA SUITE COMPLETA Y EN EL ORDEN CANONICO
=============================================================================
Los identificadores deterministas (completion, conversation, tool_call, message,
thinking, uuid4) salen de contadores GLOBALES de sesion que no se reinician
entre tests, a proposito: reiniciarlos por test hace colisionar casos distintos
y se pierden casos por deduplicacion (ronda 3 de la tarea 11).

Consecuencia directa: el valor de cada identificador depende de cuantos se
generaron antes en la MISMA sesion de pytest. Por tanto:

  - Grabar un subconjunto de tests (`pytest tests/unit/test_x.py`, `-k`, `--lf`)
    produce identificadores DISTINTOS y un corpus que NO se puede comparar con
    el corpus commiteado. No lo hagas: regenera siempre con `task corpus`.
  - Cualquier cosa que altere el orden de ejecucion (plugin que aleatoriza o
    paraleliza, otra version de pytest con otro orden de coleccion) desplaza
    todos los identificadores aunque el codigo no haya cambiado.

Contra lo primero no hay defensa tecnica posible, solo este aviso. Contra lo
segundo hay dos: `pytest_collection_modifyitems` mas abajo impone un orden
canonico propio con trylast, de modo que gana al de cualquier plugin, y la
tarea `corpus:record` del Taskfile bloquea los plugins de aleatorizacion y
paralelismo por linea de comandos.
"""

from __future__ import annotations

import base64
import dataclasses
import enum
import functools
import hashlib
import importlib
import inspect
import json
import os
import re
import sys
from collections import Counter
from pathlib import Path

import pytest

import targets as targets_module

OUT = Path(os.environ.get("CORPUS_OUT", "testdata")).resolve()
COMMIT = os.environ.get("CORPUS_COMMIT", "unknown")
MAX_PER_TARGET = int(os.environ.get("CORPUS_MAX_PER_TARGET", "500"))

_skips: Counter[str] = Counter()


# ----------------------------------------------------------------------------
# Serializacion
# ----------------------------------------------------------------------------

class Unserializable(Exception):
    """El valor no se puede representar en el corpus."""


# Pydantic llega por FastAPI. La version instalada en el venv del upstream es la 2
# (ver tools/corpus/requirements.lock), asi que el camino normal es model_dump();
# _pydantic_dump deja el .dict() de la v1 como respaldo por si el lockfile cambia.
try:
    from pydantic import BaseModel as _PydanticBaseModel
except ImportError:  # pragma: no cover - el upstream siempre trae pydantic
    _PydanticBaseModel = None


def _pydantic_dump(obj):
    """Vuelca un modelo pydantic a dict plano, recursivo en los modelos anidados.

    Sin marcador a proposito: un modelo pydantic ES un objeto JSON, y su forma
    natural en el corpus es el objeto plano, que es justo lo que el port en Go va
    a recibir por la red.

    Sin exclude_none ni exclude_unset, para calcar lo que hace el upstream: todas
    sus llamadas son `msg.model_dump()` a secas (routes_openai.py,
    routes_anthropic.py, mcp_tools.py), asi que los campos con valor nulo salen
    en el volcado.

    El volcado va en modo python, no en modo json: los modelos anidados ya salen
    como dict, y lo que queda (enum, set, bytes, tuple) lo codifica _default con
    las mismas reglas que el resto del corpus, en vez de con las de pydantic.
    """
    dump = getattr(obj, "model_dump", None)  # pydantic v2
    if callable(dump):
        try:
            return dump()
        except Exception:  # noqa: BLE001 - respaldo a la API de la v1
            pass
    dump = getattr(obj, "dict", None)  # pydantic v1
    if callable(dump):
        return dump()
    raise Unserializable(f"modelo pydantic sin volcado: {type(obj).__name__}")


def _exception_dump(exc: BaseException):
    """Codifica una excepcion. CON marcador, porque una excepcion no son datos.

    La correspondencia que la fase 2 necesita es tipo de excepcion -> resultado,
    asi que la clase y su modulo son la parte importante; args y str van tambien
    porque hay clasificadores que miran el mensaje.

    `cause` sale de __cause__ y es imprescindible, no adorno: la rama de DNS de
    kiro.network_errors.classify_network_error decide por
    isinstance(error.__cause__, socket.gaierror) y saca el errno de sus args. Sin
    la causa, httpx.ConnectError("Connection failed") con y sin gaierror encadenada
    tendrian la MISMA entrada grabada y dos salidas distintas, y la deduplicacion
    se quedaria con una de las dos en silencio.
    """
    payload = {
        "type": type(exc).__name__,
        "module": type(exc).__module__,
        "args": list(exc.args),
        "str": str(exc),
    }
    if exc.__cause__ is not None:
        payload["cause"] = _exception_dump(exc.__cause__)
    return {"__exception__": payload}


def _default(obj):
    if isinstance(obj, bytes):
        return {"__bytes__": base64.b64encode(obj).decode("ascii")}
    if isinstance(obj, bytearray):
        return {"__bytes__": base64.b64encode(bytes(obj)).decode("ascii")}
    if _PydanticBaseModel is not None and isinstance(obj, _PydanticBaseModel):
        return _pydantic_dump(obj)
    if isinstance(obj, BaseException):
        return _exception_dump(obj)
    if dataclasses.is_dataclass(obj) and not isinstance(obj, type):
        return dataclasses.asdict(obj)
    if isinstance(obj, enum.Enum):
        return obj.value
    if isinstance(obj, (set, frozenset)):
        return {"__set__": sorted(obj, key=repr)}
    if isinstance(obj, tuple):
        return list(obj)
    raise Unserializable(type(obj).__name__)


def _dumps(obj) -> str:
    """Serializa preservando el orden de insercion. Lanza Unserializable si no puede."""
    try:
        return json.dumps(obj, ensure_ascii=False, default=_default)
    except Unserializable:
        raise
    except (TypeError, ValueError) as exc:
        raise Unserializable(str(exc)) from exc


def _hash(obj) -> str:
    canonical = json.dumps(
        obj, sort_keys=True, separators=(",", ":"), ensure_ascii=True, default=_default
    )
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()[:16]


def _slug(target: str) -> str:
    """kiro.converters_core:build_kiro_payload -> converters_core/build_kiro_payload"""
    module, _, name = target.partition(":")
    return f"{module.removeprefix('kiro.')}/{name}"


# ----------------------------------------------------------------------------
# Acumulacion, deduplicacion por entrada y DETECCION DE CONFLICTOS DE SALIDA
# ----------------------------------------------------------------------------
#
# La regla original del spec (§8.1) era "deduplicar por sha256 de la entrada", y
# es insuficiente: varias funciones del upstream NO son puras (leen banderas de
# configuracion a nivel de modulo, p.ej. TRUNCATION_RECOVERY o
# FAKE_REASONING_ENABLED, que los tests parchean). Con la regla original, dos
# llamadas con la MISMA entrada y salidas DISTINTAS colapsaban en un solo caso y
# la segunda se contaba como "duplicado". El criterio de terminado del spec
# (§8.5: "si todo caso del corpus pasa, hay paridad") deja de ser cierto sobre un
# corpus asi, porque afirma una correspondencia entrada -> salida que el propio
# upstream contradice.
#
# Regla vigente: al colisionar el digest de la entrada se COMPARA la salida.
#   - salidas iguales -> duplicado legitimo, se descarta;
#   - salidas distintas -> CONFLICTO, se registra en la seccion `conflicts` de
#     _report.json con la entrada y todas las variantes de salida observadas.
#
# En las secuencias el digest se calcula sobre la parte de ENTRADA de los pasos
# (metodo + argumentos) y la comparacion sobre sus salidas. Antes el digest cubria
# el paso completo, salidas incluidas, asi que dos secuencias con las mismas
# llamadas y distintas respuestas se guardaban como dos casos distintos sin que
# nada avisara: la contradiccion no se colapsaba, pero tampoco se veia.

# slug -> digest de la entrada -> {"payload", "input", "variants"}
_cases: dict[str, dict[str, dict]] = {}

# Longitud maxima de las vistas previas que van al informe. Se puede subir con
# CORPUS_PREVIEW_CHARS para diagnosticar un conflicto concreto sin tocar el codigo.
_PREVIEW_CHARS = int(os.environ.get("CORPUS_PREVIEW_CHARS", "240"))
_INPUT_INLINE_CHARS = 4000
# Entradas conflictivas detalladas por objetivo en el informe. El resumen por
# objetivo es siempre completo; esto solo acota el detalle.
_CONFLICT_DETAIL_PER_TARGET = int(os.environ.get("CORPUS_CONFLICT_DETAIL", "5"))

# nodeid del test en curso. Va en cada variante de un conflicto: saber QUE test
# produjo cada salida es lo unico que permite diagnosticarlo sin reinstrumentar.
_current_nodeid = "<fuera de un test>"


def _input_key(payload: dict):
    """Parte de ENTRADA del caso: lo que determina el digest de deduplicacion."""
    if payload.get("kind") == "sequence":
        return [
            {"method": step.get("method"), "input": step.get("input")}
            for step in payload.get("steps", [])
        ]
    return payload.get("input")


def _output_key(payload: dict):
    """Parte de SALIDA del caso: lo que se compara cuando el digest colisiona."""
    if payload.get("kind") == "sequence":
        return [step.get("output") for step in payload.get("steps", [])]
    return payload.get("output")


def _preview(value, limit: int = _PREVIEW_CHARS) -> str:
    text = json.dumps(value, ensure_ascii=False, sort_keys=True, default=_default)
    if len(text) <= limit:
        return text
    return text[:limit] + f"... (+{len(text) - limit} caracteres)"


def _write(target: str, payload: dict) -> None:
    slug = _slug(target)
    by_digest = _cases.setdefault(slug, {})

    input_key = _input_key(payload)
    output_key = _output_key(payload)
    digest = _hash(input_key)
    out_digest = _hash(output_key)

    entry = by_digest.get(digest)
    if entry is None:
        payload["recorded_at_commit"] = COMMIT
        by_digest[digest] = {
            "payload": payload,
            "input": input_key,
            # dict ordenado por insercion: la primera variante es la que se guarda.
            "variants": {
                out_digest: {
                    "count": 1,
                    "masked": _masked_hash(output_key),
                    "preview": _preview(output_key),
                    "first_test": _current_nodeid,
                }
            },
        }
        return

    variant = entry["variants"].get(out_digest)
    if variant is None:
        entry["variants"][out_digest] = {
            "count": 1,
            "masked": _masked_hash(output_key),
            "preview": _preview(output_key),
            "first_test": _current_nodeid,
        }
    else:
        variant["count"] += 1


def _flush_to_disk() -> dict:
    """Escribe el corpus y devuelve las secciones de recuento del informe.

    El recorte por objetivo se hace aqui, sobre los digests ORDENADOS, como pide
    el spec §8.1: el subconjunto elegido no depende del orden de llegada de las
    llamadas, asi que bajar el tope recorta siempre los mismos casos.
    """
    recorded: dict[str, int] = {}
    duplicates: dict[str, int] = {}
    trimmed: dict[str, int] = {}
    per_target: dict[str, dict] = {}
    detail: list[dict] = []
    detail_omitted = 0
    conflict_inputs = 0
    conflict_calls = 0

    for slug in sorted(_cases):
        by_digest = _cases[slug]
        ordered = sorted(by_digest)
        keep = ordered[:MAX_PER_TARGET]
        if len(ordered) > len(keep):
            trimmed[slug] = len(ordered) - len(keep)

        directory = OUT / slug
        directory.mkdir(parents=True, exist_ok=True)
        for digest in keep:
            text = _dumps(by_digest[digest]["payload"])
            (directory / f"{digest}.json").write_text(
                text + "\n", encoding="utf-8", newline="\n"
            )
        recorded[slug] = len(keep)

        dups = 0
        target_inputs = 0
        target_calls = 0
        target_kinds: Counter[str] = Counter()
        shown = 0
        for digest in ordered:
            entry = by_digest[digest]
            variants = list(entry["variants"].items())
            dups += variants[0][1]["count"] - 1
            if len(variants) == 1:
                continue
            discarded = sum(v["count"] for _, v in variants[1:])
            masked = {v["masked"] for _, v in variants}
            kind = "identifier_only" if len(masked) == 1 else "behavioural"
            target_inputs += 1
            target_calls += discarded
            target_kinds[kind] += 1
            if shown < _CONFLICT_DETAIL_PER_TARGET:
                shown += 1
                record = {
                    "target": slug,
                    "kind": kind,
                    "input_sha256_16": digest,
                    "in_corpus": digest in keep,
                    "kept_output_sha256_16": variants[0][0],
                    "variants": [
                        {
                            "output_sha256_16": out_digest,
                            "masked_sha256_16": v["masked"],
                            "calls": v["count"],
                            "first_test": v["first_test"],
                            "output_preview": v["preview"],
                        }
                        for out_digest, v in variants
                    ],
                }
                inline = _preview(entry["input"], _INPUT_INLINE_CHARS)
                if inline.endswith("caracteres)"):
                    record["input_preview"] = inline
                else:
                    record["input"] = entry["input"]
                detail.append(record)
            else:
                detail_omitted += 1
        if dups:
            duplicates[slug] = dups
        if target_inputs:
            per_target[slug] = {
                "inputs": target_inputs,
                "discarded_calls": target_calls,
                "kinds": dict(sorted(target_kinds.items())),
                "worst_kind": "behavioural" if target_kinds["behavioural"] else "identifier_only",
            }
            conflict_inputs += target_inputs
            conflict_calls += target_calls

    for slug, count in sorted(trimmed.items()):
        _skips[f"{slug}: recortado por el tope de {MAX_PER_TARGET} casos"] += count

    return {
        "recorded": recorded,
        "duplicates": duplicates,
        "conflicts": {
            "targets": len(per_target),
            "inputs": conflict_inputs,
            "discarded_calls": conflict_calls,
            "per_target": per_target,
            "detail": detail,
            "detail_omitted": detail_omitted,
        },
        "total_cases": sum(recorded.values()),
    }


# ----------------------------------------------------------------------------
# Huella de la configuracion efectiva del upstream
# ----------------------------------------------------------------------------
#
# kiro/config.py llama a load_dotenv() al importarse y deriva ~34 valores de
# variables de entorno. Ningun .env esta versionado, asi que sin fijar nada el
# corpus depende del entorno de quien lo genere: es una ENTRADA OCULTA. La tarea
# corpus:record del Taskfile fija todas esas variables a los valores por defecto
# del upstream, y esta huella deja constancia en _report.json de con que
# configuracion se grabo, para que una divergencia futura se pueda diagnosticar
# en vez de adivinar.

# Nombres cuyo valor NO se volca: son credenciales o rutas locales. Se sustituyen
# por si estaban puestos o no, de modo que la huella sea comparable entre
# maquinas y no filtre secretos al repositorio.
_CONFIG_SECRET_HINTS = ("TOKEN", "KEY", "ARN", "CREDS", "SECRET", "PASSWORD", "PROXY_URL")


def _is_secretish(name: str) -> bool:
    return any(hint in name for hint in _CONFIG_SECRET_HINTS)


def _dotenv_candidates() -> list[str]:
    """Ficheros .env que el upstream cargaria, en el orden en que los busca.

    load_dotenv() sin argumentos sube desde el directorio de kiro/config.py, y
    _get_raw_env_value lee ".env" relativo al directorio de trabajo. Los dos
    caminos meten valores sin fijar en la grabacion, asi que se comprueban ambos.
    """
    found: list[str] = []
    try:
        config_module = importlib.import_module("kiro.config")
        start = Path(config_module.__file__).resolve().parent
    except Exception:  # noqa: BLE001 - solo diagnostico
        start = Path.cwd()
    for directory in [start, *start.parents]:
        candidate = directory / ".env"
        if candidate.is_file():
            found.append(str(candidate))
    cwd_env = (Path.cwd() / ".env").resolve()
    if cwd_env.is_file() and str(cwd_env) not in found:
        found.append(str(cwd_env))
    return found


def _config_fingerprint() -> dict:
    """Volcado de kiro.config tal como quedo al importarse, antes de los tests."""
    try:
        config_module = importlib.import_module("kiro.config")
    except ImportError as exc:  # pragma: no cover - el upstream siempre lo trae
        return {"error": f"no se pudo importar kiro.config: {exc}"}

    values: dict[str, object] = {}
    for name in sorted(vars(config_module)):
        if not re.fullmatch(r"[A-Z][A-Z0-9_]*", name):
            continue
        value = getattr(config_module, name)
        if not isinstance(value, (bool, int, float, str, list, dict)):
            continue
        if _is_secretish(name):
            values[name] = "<puesto>" if value else "<vacio>"
        else:
            values[name] = value

    # Variables de entorno que config.py consulta, y si estaban definidas.
    env_names: list[str] = []
    try:
        source = Path(config_module.__file__).read_text(encoding="utf-8")
        env_names = sorted(
            set(re.findall(r'(?:os\.getenv|_get_raw_env_value)\(\s*"([A-Z][A-Z0-9_]*)"', source))
        )
    except OSError:
        pass
    env_view = {
        name: ("<puesto>" if _is_secretish(name) else os.environ[name])
        for name in env_names
        if name in os.environ
    }

    digest = hashlib.sha256(
        json.dumps(values, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode()
    ).hexdigest()[:16]

    return {
        "sha256_16": digest,
        "env_vars_read_by_config": len(env_names),
        "env_vars_set": env_view,
        "env_vars_unset": [name for name in env_names if name not in os.environ],
        "dotenv_files_found": _dotenv_candidates(),
        "values": values,
    }


_config_snapshot: dict = {}


# ----------------------------------------------------------------------------
# Banderas de configuracion como parte de la ENTRADA del caso
# ----------------------------------------------------------------------------

def _flag_snapshot(module, module_name: str) -> dict:
    """Valores vigentes de las banderas declaradas para el modulo del objetivo.

    Se lee del propio modulo cuando tiene la bandera como global (es lo que ve la
    funcion, y lo que parchean los tests), y de kiro.config cuando no, que es el
    caso de los `from kiro.config import X` dentro del cuerpo de una funcion.
    """
    names = targets_module.CONFIG_INPUTS.get(module_name, ())
    if not names:
        return {}
    config_module = sys.modules.get("kiro.config")
    snapshot = {}
    for name in names:
        if hasattr(module, name):
            snapshot[name] = getattr(module, name)
        elif config_module is not None and hasattr(config_module, name):
            snapshot[name] = getattr(config_module, name)
    return snapshot


# ----------------------------------------------------------------------------
# Clasificacion de conflictos: divergencia solo de identificadores vs de conducta
# ----------------------------------------------------------------------------
#
# Los identificadores deterministas salen de contadores GLOBALES de sesion, asi que
# dos llamadas con la misma entrada emiten streams identicos salvo el id. Eso
# produce un conflicto real (misma entrada, salida distinta) que NO es una
# contradiccion de conducta del upstream, y hay que poder distinguirlo de una que
# si lo sea. Se comparan las salidas otra vez con los identificadores enmascarados:
#   - coinciden      -> "identifier_only": la fase 2 tiene que normalizar los ids
#                       de la salida antes de comparar (o inyectar los suyos);
#   - no coinciden   -> "behavioural": el upstream de verdad da dos salidas para la
#                       misma entrada, y el caso no vale como golden tal cual.
_ID_RE = re.compile(r"(chatcmpl-|msg_|sig_|call_|toolu_|srvtoolu_)[0-9a-f]{4,}")
_UUID_RE = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")


def _masked_hash(obj) -> str:
    canonical = json.dumps(
        obj, sort_keys=True, separators=(",", ":"), ensure_ascii=True, default=_default
    )
    canonical = _ID_RE.sub(r"\1<id>", canonical)
    canonical = _UUID_RE.sub("<uuid>", canonical)
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()[:16]


# ----------------------------------------------------------------------------
# Modo funcion
# ----------------------------------------------------------------------------

def _record_function(target: str, args, kwargs, result, config: dict) -> None:
    try:
        entry = {"args": list(args), "kwargs": dict(kwargs)}
        if config:
            # Las banderas van DENTRO de input: son entrada de la funcion, aunque no
            # lleguen por argumento. Ver targets_module.CONFIG_INPUTS.
            entry["config"] = config
        payload = {
            "target": target,
            "kind": "function",
            "input": json.loads(_dumps(entry)),
            "output": json.loads(_dumps(result)),
        }
    except Unserializable as exc:
        _skips[f"{_slug(target)}: {exc}"] += 1
        return
    _write(target, payload)


def _wrap_function(module, name: str):
    original = getattr(module, name)
    if getattr(original, "_corpus_wrapped", False):
        return original
    target = f"{module.__name__}:{name}"

    @functools.wraps(original)
    def wrapper(*args, **kwargs):
        # Las banderas se leen ANTES de llamar, que es cuando la funcion las lee.
        config = _flag_snapshot(module, module.__name__)
        result = original(*args, **kwargs)
        _record_function(target, args, kwargs, result, config)
        return result

    wrapper._corpus_wrapped = True
    wrapper._corpus_original = original
    return wrapper


def _rebind_everywhere(original, replacement) -> None:
    """Sustituye original por replacement en TODOS los modulos que lo referencian.

    Necesario porque el upstream hace `from kiro.x import y`: parchear solo el
    modulo de origen no afecta a las referencias ya enlazadas en otros modulos.
    """
    for mod in list(sys.modules.values()):
        if mod is None or not hasattr(mod, "__dict__"):
            continue
        for attr, value in list(vars(mod).items()):
            if value is original:
                try:
                    setattr(mod, attr, replacement)
                except (AttributeError, TypeError):
                    pass


def _install_functions() -> None:
    for module_name, func_name in targets_module.FUNCTIONS:
        try:
            module = importlib.import_module(module_name)
        except ImportError as exc:
            _skips[f"{module_name}: no se pudo importar ({exc})"] += 1
            continue
        original = getattr(module, func_name, None)
        if original is None:
            _skips[f"{module_name}:{func_name}: no existe"] += 1
            continue
        wrapper = _wrap_function(module, func_name)
        _rebind_everywhere(original, wrapper)


# ----------------------------------------------------------------------------
# Modo secuencia
# ----------------------------------------------------------------------------

_live_sequences: list = []

# Atributos que el grabador cuelga de cada instancia. Un solo subrayado a
# proposito: el doble subrayado activaria el mangling de nombres de Python y el
# atributo real pasaria a llamarse _Clase__corpus_steps, distinto en cada clase.
_STEPS_ATTR = "_corpus_steps"
_TARGET_ATTR = "_corpus_target"

# EL CONSTRUCTOR ES EL PRIMER PASO DE LA SECUENCIA.
#
# Antes solo se grababan los metodos declarados, y la construccion no se grababa en
# absoluto. Eso dejaba fuera parte de la entrada: ThinkingParser(handling_mode=...)
# y ThinkingParser() ejecutan las mismas llamadas con conducta distinta, y sus
# casos eran indistinguibles (era uno de los conflictos detectados). El
# constructor va como un paso mas, con nombre "__init__", output nulo y sus
# banderas de configuracion en input.config: no hace falta ninguna clave nueva en
# el envoltorio, y la fase 2 lee del paso 0 como construir el objeto.
_INIT_STEP = "__init__"


def _install_sequences() -> None:
    for module_name, class_name, methods in targets_module.SEQUENCES:
        try:
            module = importlib.import_module(module_name)
        except ImportError as exc:
            _skips[f"{module_name}: no se pudo importar ({exc})"] += 1
            continue
        cls = getattr(module, class_name, None)
        if cls is None:
            _skips[f"{module_name}:{class_name}: no existe"] += 1
            continue

        target = f"{module_name}:{class_name}"
        original_init = cls.__init__

        @functools.wraps(original_init)
        def patched_init(
            self, *args, _orig=original_init, _target=target, _module=module, **kwargs
        ):
            config = _flag_snapshot(_module, _module.__name__)
            _orig(self, *args, **kwargs)
            init_input = {"args": list(args), "kwargs": dict(kwargs)}
            if config:
                init_input["config"] = config
            try:
                step = {
                    "method": _INIT_STEP,
                    "input": json.loads(_dumps(init_input)),
                    "output": None,
                }
            except Unserializable as exc:
                step = {"method": _INIT_STEP, "unserializable": str(exc)}
            setattr(self, _STEPS_ATTR, [step])
            setattr(self, _TARGET_ATTR, _target)
            _live_sequences.append(self)

        cls.__init__ = patched_init

        for method_name in methods:
            original_method = getattr(cls, method_name, None)
            if original_method is None:
                _skips[f"{target}.{method_name}: no existe"] += 1
                continue

            @functools.wraps(original_method)
            def patched(self, *args, _orig=original_method, _name=method_name, **kwargs):
                result = _orig(self, *args, **kwargs)
                steps = getattr(self, _STEPS_ATTR, None)
                if steps is not None:
                    try:
                        steps.append(
                            {
                                "method": _name,
                                "input": json.loads(
                                    _dumps({"args": list(args), "kwargs": dict(kwargs)})
                                ),
                                "output": json.loads(_dumps(result)),
                            }
                        )
                    except Unserializable as exc:
                        steps.append({"method": _name, "unserializable": str(exc)})
                return result

            setattr(cls, method_name, patched)


def _flush_sequences() -> None:
    for obj in _live_sequences:
        steps = getattr(obj, _STEPS_ATTR, None)
        target = getattr(obj, _TARGET_ATTR, None)
        # steps siempre trae el paso __init__; con solo ese, la instancia se
        # construyo y nadie la uso, y no hay nada que comparar.
        if not steps or len(steps) <= 1 or target is None:
            continue
        if any("unserializable" in step for step in steps):
            _skips[f"{_slug(target)}: paso no serializable"] += 1
            continue
        _write(target, {"target": target, "kind": "sequence", "steps": steps})
    _live_sequences.clear()


# ----------------------------------------------------------------------------
# Modo generador
# ----------------------------------------------------------------------------

def _describe_unrecorded(bound, scalar_names) -> dict:
    """Anota el tipo de los argumentos que no se graban, como pista diagnostica."""
    notes = {}
    for name, value in bound.arguments.items():
        if name in scalar_names or name == "self":
            continue
        notes[name] = type(value).__name__
    return notes


def _install_generators() -> None:
    for module_name, func_name, scalar_names in targets_module.GENERATORS:
        try:
            module = importlib.import_module(module_name)
        except ImportError as exc:
            _skips[f"{module_name}: no se pudo importar ({exc})"] += 1
            continue
        original = getattr(module, func_name, None)
        if original is None:
            _skips[f"{module_name}:{func_name}: no existe"] += 1
            continue

        target = f"{module_name}:{func_name}"
        signature = inspect.signature(original)

        @functools.wraps(original)
        async def wrapper(
            *args,
            _orig=original,
            _target=target,
            _module=module,
            _sig=signature,
            _scalars=tuple(scalar_names),
            **kwargs,
        ):
            events: list = []
            upstream = getattr(_module, "parse_kiro_stream", None)

            if upstream is not None:
                async def tee(*a, **k):
                    # Como TERMINA el stream de Kiro es entrada, no adorno: varios
                    # tests hacen que su mock reviente a mitad (RuntimeError, o un
                    # GeneratorExit para simular que el cliente se desconecta) y el
                    # generador responde con un stream distinto. Sin capturar ese
                    # final, dos llamadas con los mismos eventos y distinto desenlace
                    # tenian la MISMA entrada grabada y salidas distintas.
                    #
                    # Se itera a mano a proposito. Con `async for ...: yield` no se
                    # puede distinguir un GeneratorExit que lanza el upstream de uno
                    # que nos lanza nuestro consumidor al cerrarnos: los dos caen en
                    # el mismo except. Iterando a mano, lo que lanza el upstream sale
                    # de `__anext__` y lo del consumidor llega al `yield`, que esta
                    # fuera del try.
                    iterator = upstream(*a, **k).__aiter__()
                    while True:
                        try:
                            event = await iterator.__anext__()
                        except StopAsyncIteration:
                            return
                        except BaseException as exc:  # noqa: BLE001 - se reenvia intacta
                            events.append(exc)
                            raise
                        events.append(event)
                        yield event

                setattr(_module, "parse_kiro_stream", tee)

            chunks: list = []
            # "abandoned": el consumidor corto el bucle antes del final, asi que
            # `chunks` es un prefijo del stream y no vale como salida golden.
            outcome = "abandoned"
            config = _flag_snapshot(_module, _module.__name__)
            try:
                async for out in _orig(*args, **kwargs):
                    chunks.append(out)
                    yield out
                outcome = "complete"
            except GeneratorExit:
                raise
            except BaseException:  # noqa: BLE001 - se reenvia intacta
                outcome = "raised"
                raise
            finally:
                if upstream is not None:
                    setattr(_module, "parse_kiro_stream", upstream)
                if outcome == "abandoned":
                    _skips[
                        f"{_slug(_target)}: el consumidor abandono el stream a medias"
                    ] += 1
                else:
                    try:
                        bound = _sig.bind_partial(*args, **kwargs)
                        scalars = {
                            name: value
                            for name, value in bound.arguments.items()
                            if name in _scalars
                        }
                        entry = {"kwargs": scalars, "events": events}
                        if config:
                            entry["config"] = config
                        payload = {
                            "target": _target,
                            "kind": "generator",
                            "input": json.loads(_dumps(entry)),
                            "output": json.loads(_dumps(chunks)),
                            "notes": {
                                "unrecorded_args": _describe_unrecorded(bound, _scalars),
                                "outcome": outcome,
                            },
                        }
                    except Unserializable as exc:
                        _skips[f"{_slug(_target)}: {exc}"] += 1
                    else:
                        _write(_target, payload)

        _rebind_everywhere(original, wrapper)
        setattr(module, func_name, wrapper)


# ----------------------------------------------------------------------------
# Identificadores y tiempo deterministas para grabacion
# ----------------------------------------------------------------------------

_id_counters: dict[str, int] = {}

# Tiempo congelado para la grabacion: 2024-01-01T12:00:00Z (mismo que los tests del upstream)
FROZEN_TIME = 1704110400.0


def _deterministic_completion_id() -> str:
    """Sustituto determinista de kiro.utils.generate_completion_id."""
    _id_counters["completion"] = _id_counters.get("completion", 0) + 1
    return f"chatcmpl-{_id_counters['completion']:032x}"


def _deterministic_tool_call_id() -> str:
    """Sustituto determinista de kiro.utils.generate_tool_call_id."""
    _id_counters["tool_call"] = _id_counters.get("tool_call", 0) + 1
    return f"call_{_id_counters['tool_call']:08x}"


def _deterministic_message_id() -> str:
    """Sustituto determinista de kiro.streaming_anthropic.generate_message_id."""
    _id_counters["message"] = _id_counters.get("message", 0) + 1
    return f"msg_{_id_counters['message']:024x}"


def _deterministic_thinking_signature() -> str:
    """Sustituto determinista de kiro.streaming_anthropic.generate_thinking_signature."""
    _id_counters["thinking"] = _id_counters.get("thinking", 0) + 1
    return f"sig_{_id_counters['thinking']:032x}"


class _FrozenTime:
    """Sustituto del módulo time que congela time() y delega todo lo demás."""
    def __init__(self, real_time):
        self._real = real_time

    def time(self):
        return FROZEN_TIME

    def __getattr__(self, name):
        return getattr(self._real, name)


def _deterministic_uuid4():
    """Sustituto determinista de uuid.uuid4 para los IDs que el upstream construye en linea.

    El contador va en los 32 bits altos para que los recortes que hace el upstream
    (hex[:8], hex[:24], hex[:32]) sigan siendo distintos entre llamadas.
    """
    import uuid as real_uuid

    _id_counters["uuid4"] = _id_counters.get("uuid4", 0) + 1
    n = _id_counters["uuid4"]
    return real_uuid.UUID(int=(n << 96) | n, version=4)


class _DeterministicUuid:
    """Sustituto del módulo uuid que hace uuid4() determinista y delega todo lo demás."""
    def __init__(self, real_uuid):
        self._real = real_uuid

    def uuid4(self):
        return _deterministic_uuid4()

    def __getattr__(self, name):
        return getattr(self._real, name)


# Modulos que usan time.time() para sellos de tiempo en respuestas, no para
# logica de negocio. kiro.cache y kiro.account_manager quedan fuera a proposito:
# usan time.time() para TTL y congelarlos rompe tests del upstream.
_TIME_FROZEN_MODULES = (
    "kiro.streaming_openai",   # created en chunks
    "kiro.models_openai",      # created en respuestas
    "kiro.mcp_tools",          # timestamp en herramientas MCP
    "kiro.truncation_state",   # timestamp en estado de truncamiento
)

# Modulos que construyen identificadores en linea con uuid.uuid4(), sin pasar por
# los generadores de kiro.utils ni de kiro.streaming_anthropic:
#   mcp_tools:316,710                msg_{uuid4().hex[:24]}  -> entra en format_sse_event
#   mcp_tools:126                    srvtoolu_{uuid4().hex[:32]}
#   streaming_anthropic:346,551,789  toolu_{uuid4().hex[:24]}
# kiro.utils esta aqui por generate_conversation_id: NO es aleatoria (es un sha256
# estable del historial) salvo en su rama sin mensajes, que cae a uuid4. Se graba
# como objetivo, asi que en vez de sustituir la funcion entera se hace determinista
# solo esa rama.
_UUID_PATCHED_MODULES = (
    "kiro.mcp_tools",
    "kiro.streaming_anthropic",
    "kiro.utils",
)


def _apply_lazy_patches() -> None:
    """Congela time.time() y uuid.uuid4() en los modulos de kiro ya importados.

    Se llama en cada test y es idempotente: un modulo importado tarde tambien
    queda parcheado, en vez de escaparse porque no estaba en sys.modules la
    primera vez.
    """
    import time as real_time
    import uuid as real_uuid

    for mod_name in _TIME_FROZEN_MODULES:
        mod = sys.modules.get(mod_name)
        if mod is None:
            continue
        try:
            if getattr(mod, "time", None) is real_time:
                mod.time = _FrozenTime(real_time)
        except (AttributeError, TypeError):
            pass

    for mod_name in _UUID_PATCHED_MODULES:
        mod = sys.modules.get(mod_name)
        if mod is None:
            continue
        try:
            if getattr(mod, "uuid", None) is real_uuid:
                mod.uuid = _DeterministicUuid(real_uuid)
        except (AttributeError, TypeError):
            pass


def _install_deterministic_ids() -> None:
    """Sustituye los generadores de IDs por versiones deterministas."""
    try:
        import kiro.utils
        original_completion = kiro.utils.generate_completion_id
        original_tool_call = kiro.utils.generate_tool_call_id

        kiro.utils.generate_completion_id = _deterministic_completion_id
        kiro.utils.generate_tool_call_id = _deterministic_tool_call_id

        _rebind_everywhere(original_completion, _deterministic_completion_id)
        _rebind_everywhere(original_tool_call, _deterministic_tool_call_id)
    except ImportError:
        pass

    try:
        import kiro.streaming_anthropic
        original_message = kiro.streaming_anthropic.generate_message_id
        original_thinking = kiro.streaming_anthropic.generate_thinking_signature

        kiro.streaming_anthropic.generate_message_id = _deterministic_message_id
        kiro.streaming_anthropic.generate_thinking_signature = _deterministic_thinking_signature

        _rebind_everywhere(original_message, _deterministic_message_id)
        _rebind_everywhere(original_thinking, _deterministic_thinking_signature)
    except ImportError:
        pass


# ----------------------------------------------------------------------------
# Hooks de pytest
# ----------------------------------------------------------------------------

# Huella del orden de ejecucion realmente usado. Se escribe en _report.json para
# que dos grabaciones se puedan comparar tambien por el orden, no solo por los
# bytes de los casos.
_order_digest = "sin coleccion"
_order_count = 0


def _order_key(item) -> tuple:
    """Clave del orden canonico de ejecucion.

    (fichero, linea de declaracion, indices de parametrizacion, nodeid)

    No depende del orden en que pytest o un plugin entreguen los tests, asi que
    reconstruye siempre la misma secuencia:

      - `item.location` da el fichero y la linea REAL de declaracion, ya
        desenvueltos los decoradores; `item.function.__code__` no sirve, porque
        con @patch de unittest.mock apunta a la linea del envoltorio en mock.py y
        desordena las clases decoradas;
      - los indices del callspec ordenan los casos parametrizados por su posicion
        en la lista de argvalues, igual que pytest, no alfabeticamente por su id;
      - el nodeid desempata lo que quede.

    El resultado coincide con el orden natural de pytest para esta suite, asi que
    fijarlo no cambia el corpus ya grabado; lo que hace es blindarlo.
    """
    location = getattr(item, "location", None) or ("", 0, "")
    path = str(location[0]).replace("\\", "/")
    lineno = location[1] if isinstance(location[1], int) else 0
    callspec = getattr(item, "callspec", None)
    indices = tuple(sorted(getattr(callspec, "indices", {}).items())) if callspec else ()
    return (path, lineno, indices, item.nodeid)

@pytest.hookimpl(trylast=True)
def pytest_collection_modifyitems(session, config, items):
    """Impone el orden canonico DESPUES de cualquier otro plugin.

    Los identificadores deterministas vienen de contadores globales de sesion, de
    modo que su valor depende del orden de ejecucion (ver la cabecera del
    modulo). Con trylast este hook se ejecuta al final de la cadena, asi que un
    plugin que aleatorice el orden queda anulado en lugar de cambiar el corpus en
    silencio.
    """
    global _order_digest, _order_count
    items.sort(key=_order_key)
    _order_count = len(items)
    _order_digest = hashlib.sha256(
        "\n".join(item.nodeid for item in items).encode("utf-8")
    ).hexdigest()[:16]


def pytest_configure(config):
    global _config_snapshot
    OUT.mkdir(parents=True, exist_ok=True)
    _install_deterministic_ids()
    _install_functions()
    _install_sequences()
    _install_generators()
    # Se toma DESPUES de importar los modulos de kiro y ANTES de ejecutar ningun
    # test: es el estado limpio de la configuracion. Los tests recargan config y
    # parchean banderas, asi que tomarla al final daria el estado del ultimo test.
    _config_snapshot = _config_fingerprint()
    found = _config_snapshot.get("dotenv_files_found") or []
    if found and not os.environ.get("CORPUS_ALLOW_DOTENV"):
        raise pytest.UsageError(
            "hay ficheros .env en el camino de busqueda del upstream, y sus valores "
            "entrarian en el corpus sin quedar fijados: "
            + ", ".join(found)
            + ". Quitalos, o pon CORPUS_ALLOW_DOTENV=1 si de verdad los quieres."
        )


def pytest_runtest_setup(item):
    global _current_nodeid
    _current_nodeid = item.nodeid
    # Congelar tiempo y uuid4 en los modulos de kiro que ya esten cargados.
    _apply_lazy_patches()
    # NO reiniciamos _id_counters aquí: mantener secuencia global determinista


def pytest_runtest_teardown(item, nextitem):
    _flush_sequences()


def pytest_sessionfinish(session, exitstatus):
    counts = _flush_to_disk()
    report = {
        "commit": COMMIT,
        "max_per_target": MAX_PER_TARGET,
        # Codigo de salida de la suite del upstream. La tarea corpus:record ignora
        # el fallo de pytest a proposito (un test roto no debe tirar la grabacion),
        # asi que sin esto una grabacion hecha sobre una suite en rojo era
        # indistinguible de una buena.
        "upstream_exit_status": int(exitstatus),
        # Huella del orden canonico de ejecucion. Si cambia entre dos grabaciones,
        # los identificadores deterministas se desplazan y el corpus difiere sin
        # que haya cambiado el codigo del upstream.
        "test_order": {"count": _order_count, "sha256_16": _order_digest},
        # Configuracion efectiva del upstream durante la grabacion (entrada oculta
        # si no se fija; ver _config_fingerprint).
        "config": _config_snapshot,
        "recorded": counts["recorded"],
        # Llamadas con la misma entrada Y la misma salida: duplicados legitimos.
        "duplicates": counts["duplicates"],
        # Llamadas con la misma entrada y salida DISTINTA. Ver la cabecera de
        # _write y docs/CORPUS.md: validate.py falla si aparece un objetivo
        # conflictivo que no este declarado en floors.json.
        "conflicts": counts["conflicts"],
        "skipped": dict(sorted(_skips.items())),
        "total_cases": counts["total_cases"],
    }
    (OUT / "_report.json").write_text(
        json.dumps(report, indent=2, ensure_ascii=False) + "\n",
        encoding="utf-8",
        newline="\n",
    )
    print(f"\n[corpus] {report['total_cases']} casos escritos en {OUT}")
    print(f"[corpus] orden: {_order_count} tests, huella {_order_digest}")
    print(f"[corpus] configuracion: huella {_config_snapshot.get('sha256_16')}")
    print(f"[corpus] suite del upstream: exitstatus {exitstatus}")
    conflicts = report["conflicts"]
    print(
        f"[corpus] conflictos de salida: {conflicts['inputs']} entradas en "
        f"{conflicts['targets']} objetivos, {conflicts['discarded_calls']} llamadas descartadas"
    )
    print(f"[corpus] {len(_skips)} motivos distintos de descarte; ver _report.json")
