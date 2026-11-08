#!/usr/bin/env python3
"""Validate and wave-plan a unit graph for the station/merge model.

The graph file is the single irreversible artefact of the process: once
stations start, every other decision is recoverable except the cut itself.
This tool exists so the cut can be checked mechanically before step 4 starts.

Usage:
    graph.py validate --plan .graph/plan.json
    graph.py waves    --plan .graph/plan.json [--json]

Exit codes: 0 clean, 1 invalid graph, 2 usage/IO error.

Plan schema
-----------
{
  "goal": "what the whole pass achieves",
  "base": "main",
  "test_command": "go test ./...",
  "closed_paths": ["go.mod", "scripts/graph/**", ".graph/**"],
  "migration": {"dir": "internal/db/migrations", "ranges": [[100, 139]]},
  "units": [
    {
      "id": "U1",
      "kind": "work",              # "work" | "interface"
      "task": "one sentence, provable by a test",
      "files": ["internal/authz/Foo.go"],   # files this unit may WRITE
      "reads": ["internal/authz/Bar.go"],   # files it may READ from other units
      "needs": ["U0"],                      # units that must land first
      "wave": 1,                            # output only; recomputed
      "tests_changed": false                # true only for deliberate test edits
    }
  ]
}
"""

from __future__ import annotations

import argparse
import fnmatch
import json
import re
import sys
from pathlib import Path

MIGRATION_RE = re.compile(r"^(\d{3})_.*\.sql$")
REQUIRED_UNIT_FIELDS = ("id", "task", "files")
VALID_KINDS = ("work", "interface")


def die(msg: str, code: int = 2) -> None:
    print(f"graph: {msg}", file=sys.stderr)
    raise SystemExit(code)


def load(path: Path) -> dict:
    try:
        raw = path.read_text(encoding="utf-8")
    except OSError as exc:
        die(f"cannot read plan {path}: {exc}")
    try:
        plan = json.loads(raw)
    except json.JSONDecodeError as exc:
        die(f"plan is not valid JSON: {exc}")
    if not isinstance(plan, dict):
        die("plan must be a JSON object")
    return plan


def migration_id(path: str, mig_dir: str | None) -> int | None:
    """Return the NNN id if the path is a migration inside mig_dir."""
    if not mig_dir:
        return None
    if not path.startswith(mig_dir.rstrip("/") + "/"):
        return None
    found = MIGRATION_RE.match(Path(path).name)
    return int(found.group(1)) if found else None


def inside_own_range(path: str, unit: dict, mig_dir: str | None) -> bool:
    """A migration inside the unit's reserved range is its sanctioned exception."""
    number = migration_id(path, mig_dir)
    unit_range = unit.get("migration_range")
    if number is None or not unit_range:
        return False
    return int(unit_range[0]) <= number <= int(unit_range[1])


def matches_any(path: str, patterns) -> str | None:
    for pattern in patterns or ():
        if pattern.endswith("/**"):
            prefix = pattern[:-3].rstrip("/")
            if path == prefix or path.startswith(prefix + "/"):
                return pattern
        elif fnmatch.fnmatch(path, pattern):
            return pattern
    return None


def validate(plan: dict) -> tuple[list[str], list[str]]:
    """Return (errors, warnings). Errors block the pass; warnings are the cut's debt."""
    errors: list[str] = []
    warnings: list[str] = []

    units = plan.get("units")
    if not isinstance(units, list) or not units:
        return ["plan has no units"], []

    if not plan.get("test_command"):
        errors.append("plan has no test_command; without a deterministic gate there is no station")

    closed = plan.get("closed_paths") or []
    if not closed:
        warnings.append("no closed_paths declared; nothing stops a station editing go.mod or the gate itself")

    # --- unit shape -------------------------------------------------------
    by_id: dict[str, dict] = {}
    for index, unit in enumerate(units):
        where = f"units[{index}]"
        if not isinstance(unit, dict):
            errors.append(f"{where} is not an object")
            continue
        for field in REQUIRED_UNIT_FIELDS:
            if not unit.get(field):
                errors.append(f"{where} missing {field!r}")
        uid = unit.get("id")
        if not isinstance(uid, str) or not uid:
            continue
        if uid in by_id:
            errors.append(f"duplicate unit id {uid!r}")
        by_id[uid] = unit

        kind = unit.get("kind", "work")
        if kind not in VALID_KINDS:
            errors.append(f"{uid}: kind must be one of {VALID_KINDS}, got {kind!r}")
        task = unit.get("task") or ""
        if len(task.split()) < 3:
            errors.append(f"{uid}: task must be a sentence a test can prove, got {task!r}")
        if not isinstance(unit.get("files"), list) or not unit.get("files"):
            errors.append(f"{uid}: files must be a non-empty list")

    # --- one writer per file ---------------------------------------------
    owner: dict[str, str] = {}
    for uid, unit in by_id.items():
        for path in unit.get("files") or ():
            if path in owner and owner[path] != uid:
                errors.append(f"file {path!r} is written by both {owner[path]} and {uid}; the cut is not branchable")
            owner[path] = uid

    # --- closed paths -----------------------------------------------------
    mig_dir_for_closed = (plan.get("migration") or {}).get("dir")
    for uid, unit in by_id.items():
        for path in unit.get("files") or ():
            if inside_own_range(path, unit, mig_dir_for_closed):
                continue
            hit = matches_any(path, closed)
            if hit:
                errors.append(f"{uid}: writes {path!r} which is closed by {hit!r}")

    # --- needs / reads references ----------------------------------------
    for uid, unit in by_id.items():
        for need in unit.get("needs") or ():
            if need not in by_id:
                errors.append(f"{uid}: needs unknown unit {need!r}")
            elif need == uid:
                errors.append(f"{uid}: needs itself")
        for read in unit.get("reads") or ():
            if read not in owner and not Path(read).exists():
                warnings.append(
                    f"{uid}: reads {read!r}, which no unit produces and which is not yet in the repo; "
                    "ok if another unit creates it, otherwise the reader will not compile"
                )
        for path in unit.get("files") or ():
            if path in (unit.get("reads") or ()):
                warnings.append(f"{uid}: lists {path!r} as both files and reads")

    # --- cycles -----------------------------------------------------------
    colour: dict[str, int] = {}

    def walk(uid: str, stack: list[str]) -> None:
        colour[uid] = 1
        for need in by_id.get(uid, {}).get("needs") or ():
            if need not in by_id:
                continue
            if colour.get(need) == 1:
                cycle = " -> ".join(stack[stack.index(need):] + [need]) if need in stack else f"{uid} -> {need}"
                errors.append(f"dependency cycle: {cycle}")
            elif colour.get(need) is None:
                walk(need, stack + [uid])
        colour[uid] = 2

    for uid in by_id:
        if colour.get(uid) is None:
            walk(uid, [])

    # --- signature crossings need an interface unit -----------------------
    for uid, unit in by_id.items():
        for read in unit.get("reads") or ():
            writer = owner.get(read)
            if not writer or writer == uid:
                continue
            writer_unit = by_id[writer]
            if writer_unit.get("kind", "work") == "work":
                warnings.append(
                    f"{uid} reads {read!r} owned by {writer}: if this is a type or "
                    f"signature change, {writer} must be split into an interface unit "
                    f"or {uid} cannot compile in its own lane"
                )

    # --- migration id space ----------------------------------------------
    migration = plan.get("migration") or {}
    mig_dir = migration.get("dir")
    ranges = migration.get("ranges") or []
    if mig_dir and ranges:
        used: dict[str, list[str]] = {}
        ranges_seen: list[tuple[int, int, str]] = []
        for uid, unit in by_id.items():
            unit_range = unit.get("migration_range")
            if not unit_range:
                continue
            try:
                low, high = int(unit_range[0]), int(unit_range[1])
            except (TypeError, ValueError, IndexError):
                errors.append(f"{uid}: migration_range must be [low, high] integers")
                continue
            ranges_seen.append((low, high, uid))
            allowed = any(int(lo) <= low and high <= int(hi) for lo, hi in ranges)
            if not allowed:
                errors.append(f"{uid}: migration_range {unit_range} is outside the reserved ranges {ranges}")
            for path in unit.get("files") or ():
                if not path.startswith(mig_dir.rstrip("/") + "/"):
                    continue
                found = MIGRATION_RE.match(Path(path).name)
                if not found:
                    errors.append(f"{uid}: {path!r} does not match NNN_name.sql")
                    continue
                number = int(found.group(1))
                if not (low <= number <= high):
                    errors.append(f"{uid}: {path!r} id {number} is outside its own range {unit_range}")
                used.setdefault(found.group(1), []).append(uid)
        for number, uids in sorted(used.items()):
            if len(set(uids)) > 1:
                errors.append(f"migration id {number} claimed by {sorted(set(uids))}; ids are a global sequence")
        for i, (low_a, high_a, uid_a) in enumerate(ranges_seen):
            for low_b, high_b, uid_b in ranges_seen[i + 1:]:
                if low_a <= high_b and low_b <= high_a:
                    errors.append(f"migration ranges of {uid_a} and {uid_b} overlap")
    elif any(unit.get("migration_range") for unit in by_id.values()):
        warnings.append("units declare migration_range but plan.migration.ranges is missing")

    # --- existing migration ids already taken -----------------------------
    if mig_dir and Path(mig_dir).is_dir():
        taken = sorted(
            {int(m.group(1)) for p in Path(mig_dir).glob("*.sql") if (m := MIGRATION_RE.match(p.name))}
        )
        if taken:
            dupes = {n for n in taken if taken.count(n) > 1}
            if dupes:
                warnings.append(
                    f"repo already carries duplicate migration ids {sorted(dupes)}; "
                    f"the reserved range must start above {max(taken)}"
                )
            for uid, unit in by_id.items():
                unit_range = unit.get("migration_range")
                if not unit_range:
                    continue
                clash = [n for n in taken if int(unit_range[0]) <= n <= int(unit_range[1])]
                if clash:
                    errors.append(f"{uid}: migration range clashes with existing ids {clash}")

    # --- gate integrity ---------------------------------------------------
    for uid, unit in by_id.items():
        for path in unit.get("files") or ():
            if Path(path).name in ("check.py", "graph.py", "plan.json") and not unit.get("tests_changed"):
                errors.append(f"{uid}: writes the gate itself ({path!r}) without tests_changed")

    return errors, warnings


def waves(plan: dict) -> list[list[str]]:
    units = {unit["id"]: unit for unit in plan.get("units") or [] if unit.get("id")}
    depth: dict[str, int] = {}

    def level(uid: str) -> int:
        if uid in depth:
            return depth[uid]
        needs = [n for n in units.get(uid, {}).get("needs") or () if n in units]
        depth[uid] = 0 if not needs else 1 + max(level(n) for n in needs)
        return depth[uid]

    for uid in units:
        level(uid)

    buckets: dict[int, list[str]] = {}
    for uid, index in depth.items():
        buckets.setdefault(index, []).append(uid)
    return [sorted(buckets[i]) for i in sorted(buckets)]


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="graph.py", description=__doc__.splitlines()[0])
    parser.add_argument("command", choices=("validate", "waves"))
    parser.add_argument("--plan", default=".graph/plan.json")
    parser.add_argument("--json", action="store_true")
    parser.add_argument("--strict", action="store_true", help="treat warnings as errors")
    args = parser.parse_args(argv)

    plan = load(Path(args.plan))
    errors, warnings = validate(plan)
    if args.strict and warnings:
        errors = errors + [f"strict: {warning}" for warning in warnings]

    if args.command == "validate":
        if args.json:
            print(json.dumps({"ok": not errors, "errors": errors, "warnings": warnings}, indent=2))
        else:
            for warning in warnings:
                print(f"WARN  {warning}")
            for error in errors:
                print(f"ERROR {error}")
            print(f"{len(errors)} error(s), {len(warnings)} warning(s)")
        return 0 if not errors else 1

    if errors:
        for error in errors:
            print(f"ERROR {error}", file=sys.stderr)
        print("refusing to plan waves on an invalid graph", file=sys.stderr)
        return 1

    units = {unit["id"]: unit for unit in plan["units"]}
    plan_waves = []
    for index, group in enumerate(waves(plan), start=1):
        plan_waves.append(
            {
                "wave": index,
                "units": [
                    {
                        "id": uid,
                        "kind": units[uid].get("kind", "work"),
                        "task": units[uid].get("task"),
                        "files": units[uid].get("files", []),
                        "reads": units[uid].get("reads", []),
                        # A unit can only be gated if it starts from a clean base
                        # with no other lane's work in flight.
                        "gated_eligible": not (units[uid].get("needs") or []),
                    }
                    for uid in group
                ],
            }
        )

    if args.json:
        print(json.dumps({"waves": plan_waves, "warnings": warnings}, indent=2))
    else:
        for warning in warnings:
            print(f"WARN  {warning}")
        for entry in plan_waves:
            print(f"\nwave {entry['wave']} ({len(entry['units'])} station(s))")
            for unit in entry["units"]:
                gate = "GATED eligible" if unit["gated_eligible"] else "held for deps"
                print(f"  {unit['id']:<6} [{unit['kind']}] {gate}: {unit['task']}")
                for path in unit["files"]:
                    print(f"         writes {path}")
                for path in unit["reads"]:
                    print(f"         reads  {path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
