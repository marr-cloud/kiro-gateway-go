"""Copia y compara instantaneas del corpus, sin depender de cp ni diff.

Uso:
    python tools/corpus/snapshot.py save testdata .corpus-check
    python tools/corpus/snapshot.py compare .corpus-check testdata
    python tools/corpus/snapshot.py variance GEN1 GEN2 [GEN3 ...]
    python tools/corpus/snapshot.py drop .corpus-check

`compare` contrasta dos instantaneas y falla al primer byte distinto.
`variance` contrasta N instantaneas (N >= 2) y da el desglose por objetivo: es la
evidencia reproducible de que varias generaciones seguidas salen identicas, y
senala exactamente que objetivo es inestable cuando no lo son.
"""

from __future__ import annotations

import hashlib
import shutil
import sys
from pathlib import Path

IGNORED = {"_report.json"}


def _files(root: Path) -> dict[str, bytes]:
    result = {}
    for path in sorted(root.rglob("*.json")):
        if path.name in IGNORED:
            continue
        key = str(path.relative_to(root)).replace("\\", "/")
        result[key] = path.read_bytes()
    return result


def _target_of(key: str) -> str:
    """converters_core/build_kiro_payload/ab12.json -> converters_core/build_kiro_payload"""
    return key.rsplit("/", 1)[0] if "/" in key else "."


def compare(left: Path, right: Path) -> int:
    a, b = _files(left), _files(right)

    # Sin este guardia la comprobacion pasa en vacio: un grabador roto deja las
    # dos instantaneas a cero y compare informa de "0 ficheros identicos".
    if not a or not b:
        print(f"{left}: {len(a)} casos, {right}: {len(b)} casos")
        print("\ninstantanea vacia: la comprobacion no prueba nada, el grabador no genero corpus")
        return 1

    only_left = sorted(a.keys() - b.keys())
    only_right = sorted(b.keys() - a.keys())
    differing = sorted(k for k in a.keys() & b.keys() if a[k] != b[k])

    for key in only_left[:20]:
        print(f"solo en {left}: {key}")
    for key in only_right[:20]:
        print(f"solo en {right}: {key}")
    for key in differing[:20]:
        print(f"contenido distinto: {key}")

    total = len(only_left) + len(only_right) + len(differing)
    if total:
        print(f"\n{total} diferencias: el corpus NO es determinista")
        return 1
    print(f"corpus determinista: {len(a)} ficheros identicos")
    return 0


def variance(roots: list[Path]) -> int:
    """Compara N instantaneas y desglosa la varianza por objetivo.

    Sustituye al script de un solo uso con el que se comprobo el determinismo en
    la tarea 11: aquello no quedo versionado y su evidencia no se podia
    reproducir. Esto si.
    """
    missing = [str(root) for root in roots if not root.is_dir()]
    if missing:
        print(f"ERROR: no existen estas instantaneas: {', '.join(missing)}")
        return 1

    snapshots = [_files(root) for root in roots]

    print(f"instantaneas: {len(roots)}")
    for root, files in zip(roots, snapshots):
        print(f"  {root}: {len(files)} casos")
    print()

    empty = [str(root) for root, files in zip(roots, snapshots) if not files]
    if empty:
        print(f"instantanea vacia ({', '.join(empty)}): la comprobacion no prueba nada")
        return 1

    targets = sorted({_target_of(key) for files in snapshots for key in files})

    unstable_targets: list[str] = []
    unstable_files = 0
    print(f"{'objetivo':<62}{'casos':>7}{'variantes':>11}{'inestables':>12}")
    for target in targets:
        keys = sorted({k for files in snapshots for k in files if _target_of(k) == target})
        # Una "variante" es una version distinta del conjunto completo de ficheros
        # del objetivo: 1 variante significa que las N instantaneas coinciden.
        signatures = {
            hashlib.sha256(
                "".join(
                    f"{k}:{hashlib.sha256(files[k]).hexdigest()};" if k in files else f"{k}:-;"
                    for k in keys
                ).encode("ascii")
            ).hexdigest()
            for files in snapshots
        }
        differing = [
            k
            for k in keys
            if len({files.get(k) for files in snapshots}) > 1
        ]
        counts = {len([k for k in files if _target_of(k) == target]) for files in snapshots}
        shown = str(min(counts)) if len(counts) == 1 else f"{min(counts)}-{max(counts)}"
        mark = "" if len(signatures) == 1 else "  <-- INESTABLE"
        print(f"{target:<62}{shown:>7}{len(signatures):>11}{len(differing):>12}{mark}")
        if len(signatures) > 1:
            unstable_targets.append(target)
            unstable_files += len(differing)

    total_cases = {len(files) for files in snapshots}
    print()
    print(f"objetivos: {len(targets)}")
    print(f"casos:     {min(total_cases) if len(total_cases) == 1 else sorted(total_cases)}")

    if unstable_targets:
        print(f"\n{len(unstable_targets)} objetivos inestables, {unstable_files} ficheros distintos")
        for target in unstable_targets[:20]:
            print(f"  - {target}")
        print("\nel corpus NO es determinista")
        return 1

    print(f"\nlas {len(roots)} instantaneas son identicas: el corpus es determinista")
    return 0


USAGE = "Uso: snapshot.py {save SRC DST | compare A B | variance A B [C ...] | drop DIR}"


def main(argv: list[str]) -> int:
    if not argv:
        print(__doc__)
        return 2
    command, rest = argv[0], argv[1:]

    if command == "drop":
        if len(rest) != 1:
            print(f"drop necesita exactamente un directorio.\n{USAGE}")
            return 2
        shutil.rmtree(Path(rest[0]), ignore_errors=True)
        return 0

    if command == "save":
        if len(rest) != 2:
            print(f"save necesita SRC y DST.\n{USAGE}")
            return 2
        src, dst = Path(rest[0]), Path(rest[1])
        shutil.rmtree(dst, ignore_errors=True)
        shutil.copytree(src, dst)
        print(f"instantanea guardada en {dst}")
        return 0

    if command == "compare":
        if len(rest) != 2:
            print(f"compare necesita dos instantaneas.\n{USAGE}")
            return 2
        return compare(Path(rest[0]), Path(rest[1]))

    if command == "variance":
        if len(rest) < 2:
            print(f"variance necesita al menos dos instantaneas.\n{USAGE}")
            return 2
        return variance([Path(arg) for arg in rest])

    print(f"comando desconocido: {command}\n{USAGE}")
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
