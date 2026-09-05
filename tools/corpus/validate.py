"""Valida el corpus generado y emite un informe de tamano.

Uso: python tools/corpus/validate.py testdata

Comprueba:
  - estructura de cada caso (claves, kind, contenido coherente);
  - que todos los casos se grabaron del commit esperado del upstream;
  - que el informe del grabador (_report.json) cuadra con los ficheros presentes,
    que la suite del upstream termino en verde y que quedo constancia de la
    configuracion efectiva;
  - LOS CONFLICTOS DE SALIDA declarados en _report.json (ver mas abajo);
  - presupuesto de tamano total y tope de casos por objetivo;
  - los minimos declarados en floors.json (total, numero de objetivos y por
    objetivo), para que la validacion no pueda pasar en vacio, ni tras excluir un
    objetivo valioso, ni tras perder un objetivo entero;
  - la correspondencia con los objetivos declarados en targets.py: un objetivo
    declarado que no graba nada tiene que estar documentado en
    known_empty_targets de floors.json, con su causa.

POLITICA DE CONFLICTOS
----------------------
Un conflicto es una entrada que el upstream contesto con dos salidas distintas
(ver la cabecera de _write en recorder.py). Aqui FALLAN, no avisan, y por dos
motivos distintos segun su clase:

  - "behavioural": el corpus afirmaria dos respuestas para la misma pregunta y
    NINGUNA fase posterior puede satisfacer las dos. No se puede declarar como
    conocido a proposito: la salida es completar la entrada grabada (banderas en
    CONFIG_INPUTS, desenlace del stream) o retirar el objetivo. Si se pudiera
    silenciar con una nota, la nota se escribiria una vez y nadie la volveria a
    leer, que es exactamente como llego el corpus a tener 3221 descartes ciegos.

  - "identifier_only": las salidas solo difieren en identificadores generados, que
    salen de contadores globales de sesion. No es una contradiccion de conducta,
    pero SI invalida la comparacion byte a byte que la fase 2 iba a hacer, asi que
    tiene que estar declarado en known_conflicting_targets de floors.json con lo
    que la fase 2 debe hacer al respecto. Un objetivo conflictivo sin declarar
    falla.

El criterio de terminado del spec (§8.5) dice que si todo caso del corpus pasa
entonces hay paridad. Un aviso no protege esa implicacion; un fallo si.
"""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

import targets as targets_module

MAX_TOTAL_BYTES = 50 * 1024 * 1024
MAX_PER_TARGET = int(os.environ.get("CORPUS_MAX_PER_TARGET", "500"))
EXPECTED_COMMIT = os.environ.get("CORPUS_EXPECTED_COMMIT", "a5292ca")

REQUIRED_KEYS = {"target", "kind", "recorded_at_commit"}
VALID_KINDS = {"function", "sequence", "generator"}

FLOORS_PATH = Path(__file__).with_name("floors.json")


def _load_floors() -> dict:
    if not FLOORS_PATH.is_file():
        return {}
    return json.loads(FLOORS_PATH.read_text(encoding="utf-8"))


def _floor_spec(value) -> tuple[int, str]:
    """Normaliza una entrada de min_per_target: entero o {"min": N, "reason": "..."}."""
    if isinstance(value, dict):
        return int(value.get("min", 0)), str(value.get("reason", "sin motivo declarado"))
    return int(value), "recuento observado en el corpus de referencia"


def _slug(target: str) -> str:
    """kiro.converters_core:build_kiro_payload -> converters_core/build_kiro_payload"""
    module, _, name = target.partition(":")
    return f"{module.removeprefix('kiro.')}/{name}"


def _declared_targets() -> list[str]:
    """Slugs de todos los objetivos declarados en targets.py."""
    declared = [f"{module}:{name}" for module, name in targets_module.FUNCTIONS]
    declared += [f"{module}:{name}" for module, name, _ in targets_module.SEQUENCES]
    declared += [f"{module}:{name}" for module, name, _ in targets_module.GENERATORS]
    return sorted({_slug(target) for target in declared})


def _check_report(
    root: Path, per_target: dict[str, int], floors: dict, problems: list[str], notes: list[str]
) -> None:
    """Contrasta _report.json con los ficheros y aplica la politica de conflictos."""
    path = root / "_report.json"
    if not path.is_file():
        problems.append(f"{path}: no existe; el corpus se genero sin el grabador o a medias")
        return
    try:
        report = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        problems.append(f"{path}: JSON invalido: {exc}")
        return

    # --- el informe tiene que cuadrar con los ficheros -------------------------
    recorded = report.get("recorded") or {}
    total = report.get("total_cases")
    actual_total = sum(per_target.values())
    if total != actual_total:
        problems.append(
            f"_report.json dice {total} casos y en disco hay {actual_total}: "
            "el corpus esta a medias o el informe es de otra generacion"
        )
    for target in sorted(set(recorded) | set(per_target)):
        said, real = recorded.get(target, 0), per_target.get(target, 0)
        if said != real:
            problems.append(
                f"{target}: _report.json dice {said} casos y en disco hay {real}"
            )

    # --- la suite del upstream tiene que haber terminado en verde --------------
    # corpus:record ignora el codigo de salida de pytest a proposito, para que un
    # test roto no tire la grabacion entera. Aqui es donde se ve.
    status = report.get("upstream_exit_status")
    if status is None:
        problems.append(
            "_report.json no trae upstream_exit_status: se grabo con un recorder anterior"
        )
    elif status != 0:
        problems.append(
            f"la suite del upstream termino con exitstatus {status}: el corpus se grabo "
            "sobre una suite en rojo y los casos afectados no son de fiar"
        )

    # --- la configuracion efectiva tiene que quedar registrada ----------------
    config = report.get("config") or {}
    if not config.get("sha256_16"):
        problems.append(
            "_report.json no trae la huella de la configuracion del upstream: la "
            "configuracion es una entrada del corpus y sin huella no se puede comparar"
        )
    else:
        print(f"config:    huella {config['sha256_16']}")
    if config.get("dotenv_files_found"):
        problems.append(
            "la grabacion vio ficheros .env ("
            + ", ".join(config["dotenv_files_found"])
            + "): sus valores entraron en el corpus sin quedar fijados"
        )

    # --- politica de conflictos ----------------------------------------------
    conflicts = report.get("conflicts")
    if conflicts is None:
        problems.append(
            "_report.json no trae la seccion conflicts: se grabo con un recorder que "
            "deduplicaba por la entrada sin comparar la salida, y no se puede saber si "
            "colapso salidas contradictorias"
        )
        return

    declared = floors.get("known_conflicting_targets", {})
    per_conflict = conflicts.get("per_target", {})
    print()
    print(
        f"conflictos: {conflicts.get('inputs', 0)} entradas en "
        f"{conflicts.get('targets', 0)} objetivos, "
        f"{conflicts.get('discarded_calls', 0)} llamadas descartadas"
    )
    for target, info in sorted(per_conflict.items()):
        kinds = info.get("kinds", {})
        worst = info.get("worst_kind", "behavioural")
        print(
            f"  {target}: {info.get('inputs', 0)} entradas, "
            f"{info.get('discarded_calls', 0)} llamadas, {kinds}"
        )
        if kinds.get("behavioural"):
            problems.append(
                f"{target}: {kinds['behavioural']} conflictos de CONDUCTA (misma entrada, "
                "salidas que no se reconcilian ni normalizando identificadores). El corpus "
                "afirmaria dos salidas para una entrada y la fase 2 no puede cumplir las "
                "dos: completa la entrada grabada (CONFIG_INPUTS en targets.py, desenlace "
                "del stream) o retira el objetivo. Esto NO se declara como conocido; ver "
                "docs/CORPUS.md"
            )
            continue
        spec = declared.get(target)
        if spec is None:
            problems.append(
                f"{target}: tiene conflictos de salida y no esta declarado en "
                "known_conflicting_targets de floors.json. Declaralo con su clase, su "
                "causa y que tiene que hacer la fase 2, o arregla la causa"
            )
            continue
        if spec.get("kind") != worst:
            problems.append(
                f"{target}: declarado como {spec.get('kind')!r} en floors.json y observado "
                f"como {worst!r}; revisa la declaracion"
            )
    for target, spec in sorted(declared.items()):
        if target not in per_conflict:
            notes.append(
                f"known_conflicting_targets tiene {target}, que ya no genera conflictos "
                f"({spec.get('reason', 'sin motivo')}): quitalo de ahi"
            )


def main(root: Path) -> int:
    if not root.is_dir():
        print(f"ERROR: {root} no existe")
        return 1

    problems: list[str] = []
    notes: list[str] = []
    floors = _load_floors()
    total_bytes = 0
    per_target: dict[str, int] = {}
    wrong_commit: dict[str, str] = {}

    for path in sorted(root.rglob("*.json")):
        if path.name == "_report.json":
            continue
        total_bytes += path.stat().st_size
        target_dir = str(path.parent.relative_to(root)).replace("\\", "/")
        per_target[target_dir] = per_target.get(target_dir, 0) + 1

        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except json.JSONDecodeError as exc:
            problems.append(f"{path}: JSON invalido: {exc}")
            continue

        missing = REQUIRED_KEYS - data.keys()
        if missing:
            problems.append(f"{path}: faltan claves {sorted(missing)}")
        if data.get("kind") not in VALID_KINDS:
            problems.append(f"{path}: kind invalido {data.get('kind')!r}")
        if data.get("kind") == "sequence" and not data.get("steps"):
            problems.append(f"{path}: secuencia sin pasos")
        if data.get("kind") in {"function", "generator"} and "input" not in data:
            problems.append(f"{path}: falta input")
        commit = data.get("recorded_at_commit")
        if commit != EXPECTED_COMMIT:
            wrong_commit.setdefault(str(commit), str(path))

    for target, count in sorted(per_target.items()):
        if count > MAX_PER_TARGET:
            problems.append(f"{target}: {count} casos, tope {MAX_PER_TARGET}")

    total_cases = sum(per_target.values())
    print(f"objetivos: {len(per_target)}")
    print(f"casos:     {total_cases}")
    print(f"tamano:    {total_bytes / 1024 / 1024:.2f} MB")
    print(f"commit:    {EXPECTED_COMMIT} (esperado en cada caso)")
    print(f"tope:      {MAX_PER_TARGET} casos por objetivo")
    _check_report(root, per_target, floors, problems, notes)
    print()
    for target, count in sorted(per_target.items(), key=lambda kv: (-kv[1], kv[0]))[:15]:
        print(f"  {count:5d}  {target}")

    if total_bytes > MAX_TOTAL_BYTES:
        problems.append(
            f"tamano total {total_bytes / 1024 / 1024:.1f} MB supera el presupuesto de 50 MB"
        )

    for commit, sample in sorted(wrong_commit.items()):
        problems.append(
            f"recorded_at_commit {commit!r} en lugar de {EXPECTED_COMMIT!r} "
            f"(ejemplo: {sample}); el corpus se grabo de otro commit del upstream"
        )

    floor_specs = {
        target: _floor_spec(value)
        for target, value in floors.get("min_per_target", {}).items()
    }

    # --- minimos por objetivo, comparados contra el tope EFECTIVO --------------
    # El brief permite bajar CORPUS_MAX_PER_TARGET si el corpus se sale del
    # presupuesto de 50 MB. Un minimo por objetivo mayor que el tope vigente seria
    # imposible de cumplir, asi que el minimo efectivo es min(minimo, tope): la
    # bajada del tope queda como NOTA, no como fallo, y el minimo sigue vigilando
    # todo lo que el tope no recorta.
    clamped = 0
    print()
    for target, (floor, reason) in sorted(floor_specs.items()):
        count = per_target.get(target, 0)
        effective = min(floor, MAX_PER_TARGET)
        if effective < floor:
            clamped += 1
        mark = "ok  " if count >= effective else "BAJO"
        suffix = f" [recortado por el tope {MAX_PER_TARGET}, declarado {floor}]" if effective < floor else ""
        print(f"  {mark} {target}: {count} casos (minimo {effective}){suffix}")
        if count < effective:
            problems.append(
                f"{target}: {count} casos, minimo declarado {effective} ({reason})"
            )
    if clamped:
        notes.append(
            f"{clamped} minimos por objetivo recortados por CORPUS_MAX_PER_TARGET="
            f"{MAX_PER_TARGET}; con el tope de referencia "
            f"{floors.get('expected_max_per_target', MAX_PER_TARGET)} se exigirian los declarados"
        )

    # --- minimo del numero de objetivos ---------------------------------------
    min_targets = floors.get("min_targets_with_cases")
    if min_targets is not None and len(per_target) < min_targets:
        problems.append(
            f"solo {len(per_target)} objetivos con casos, minimo declarado {min_targets} "
            "en floors.json: se ha perdido un objetivo entero"
        )

    # --- minimo global, tambien recortado por el tope efectivo ----------------
    min_total = floors.get("min_total_cases")
    if min_total is not None:
        effective_total = min(
            min_total, sum(min(floor, MAX_PER_TARGET) for floor, _ in floor_specs.values())
        )
        if total_cases < effective_total:
            problems.append(
                f"solo {total_cases} casos, minimo declarado {effective_total} en floors.json"
            )

    # --- objetivos declarados que no graban nada ------------------------------
    known_empty = floors.get("known_empty_targets", {})
    declared = _declared_targets()
    print()
    for target in declared:
        if per_target.get(target, 0):
            continue
        if target in known_empty:
            print(f"  vacio (documentado) {target}: {known_empty[target]}")
            continue
        problems.append(
            f"{target}: declarado en targets.py y no graba ningun caso, y no esta "
            "documentado en known_empty_targets de floors.json"
        )
    for target, reason in sorted(known_empty.items()):
        if target not in declared:
            notes.append(
                f"known_empty_targets tiene {target}, que ya no esta en targets.py: sobra"
            )
        elif per_target.get(target, 0):
            notes.append(
                f"{target} ya graba {per_target[target]} casos y sigue en "
                f"known_empty_targets ({reason}): quitalo de ahi y ponle un minimo"
            )
    for target in sorted(per_target):
        if target not in declared:
            problems.append(
                f"{target}: hay casos grabados de un objetivo que no esta en targets.py; "
                "el corpus esta desactualizado, regeneralo"
            )

    if notes:
        print("\nNOTAS:")
        for note in notes:
            print(f"  - {note}")

    if problems:
        print("\nPROBLEMAS:")
        for p in problems:
            print(f"  - {p}")
        return 1

    print("\ncorpus valido")
    return 0


if __name__ == "__main__":
    sys.exit(main(Path(sys.argv[1] if len(sys.argv) > 1 else "testdata")))
