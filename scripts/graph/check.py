#!/usr/bin/env python3
"""Deterministic per-unit gate. No model inside.

A station claims GREEN by running this. It decides from the diff and the
test output alone, never from anything the station says about itself.

Usage:
    check.py --unit U1 [--plan .graph/plan.json] [--repo .] [--json] [--base REF]

Verdicts
    GREEN  scope clean, every check passed, tests added if required.
    RED    anything else. Prints UNIT/VERDICT/REASON/EVIDENCE/SCOPE so the
           unit can be re-dispatched alone, never as a batch.

Exit codes: 0 GREEN, 1 RED, 2 the gate could not run (bad plan, bad repo).
"""

from __future__ import annotations

import argparse
import fnmatch
import json
import re
import subprocess
import sys
from pathlib import Path

MIGRATION_RE = re.compile(r"^(\d{3})_.*\.sql$")
EVIDENCE_LINES = 15


class Gate:
    def __init__(self, repo: Path) -> None:
        self.repo = repo
        self.reasons: list[str] = []
        self.scope: dict[str, object] = {}

    # --- helpers --------------------------------------------------------
    def git(self, *args: str) -> str:
        result = subprocess.run(
            ["git", *args], cwd=self.repo, capture_output=True, text=True, check=False
        )
        if result.returncode != 0:
            raise RuntimeError(f"git {' '.join(args)} failed: {result.stderr.strip()}")
        return result.stdout

    def run(self, command: list[str], timeout: int = 900) -> tuple[int, str]:
        try:
            result = subprocess.run(
                command, cwd=self.repo, capture_output=True, text=True, timeout=timeout, check=False
            )
        except FileNotFoundError:
            return 127, f"command not found: {command[0]}"
        except subprocess.TimeoutExpired:
            return 124, f"timeout after {timeout}s: {' '.join(command)}"
        return result.returncode, (result.stdout or "") + (result.stderr or "")

    def changed_files(self, base: str, ignore: tuple[str, ...] = ()) -> list[str]:
        """Files this unit touched: committed off base plus anything uncommitted.

        `ignore` drops the gate's own inputs and outputs: the plan and the
        evidence trail are not the station's work and must not be scored.
        """
        files: set[str] = set()
        try:
            merge_base = self.git("merge-base", base, "HEAD").strip() or base
        except RuntimeError:
            merge_base = base
        files.update(line for line in self.git("diff", "--name-only", merge_base, "HEAD").splitlines() if line)
        files.update(
            line for line in self.git("diff", "--name-only", merge_base).splitlines() if line
        )
        files.update(
            line for line in self.git("ls-files", "--others", "--exclude-standard").splitlines() if line
        )
        def ignored(path: str) -> bool:
            return any(path == rule or (rule.endswith("/") and path.startswith(rule)) for rule in ignore)

        return sorted(path for path in files if not ignored(path))

    def changed_statuses(self, base: str) -> dict[str, str]:
        """path -> A/M/D/R for tracked differences off base."""
        try:
            merge_base = self.git("merge-base", base, "HEAD").strip() or base
        except RuntimeError:
            merge_base = base
        status: dict[str, str] = {}
        for line in self.git("diff", "--name-status", merge_base).splitlines():
            parts = line.split("\t")
            if len(parts) >= 2:
                status[parts[-1]] = parts[0][0]
        for line in self.git("status", "--porcelain").splitlines():
            if len(line) > 3:
                code, path = line[:2].strip(), line[3:].strip()
                status.setdefault(path, "A" if code == "??" else (code[:1] or "M"))
        return status

    def fail(self, reason: str) -> None:
        self.reasons.append(reason)


def matches_any(path: str, patterns) -> str | None:
    for pattern in patterns or ():
        if pattern.endswith("/**"):
            prefix = pattern[:-3].rstrip("/")
            if path == prefix or path.startswith(prefix + "/"):
                return pattern
        elif fnmatch.fnmatch(path, pattern):
            return pattern
    return None


def inside_own_range(path: str, unit: dict, mig_dir: str | None) -> bool:
    """A migration inside the unit's reserved range is its sanctioned exception."""
    if not mig_dir or not path.startswith(mig_dir.rstrip("/") + "/"):
        return False
    found = MIGRATION_RE.match(Path(path).name)
    unit_range = unit.get("migration_range")
    if not found or not unit_range:
        return False
    return int(unit_range[0]) <= int(found.group(1)) <= int(unit_range[1])


def go_packages(files: list[str]) -> list[str]:
    """Directories holding changed .go files, as package patterns."""
    packages = set()
    for path in files:
        if path.endswith(".go"):
            directory = Path(path).parent.as_posix()
            packages.add("./" + (directory if directory != "." else ""))
    return sorted(packages)


def evaluate(gate: Gate, plan: dict, unit: dict, base: str, ignore: tuple[str, ...] = ()) -> dict:
    closed = plan.get("closed_paths") or []
    declared_files = list(unit.get("files") or [])
    changed = gate.changed_files(base, ignore)
    statuses = gate.changed_statuses(base)

    gate.scope = {
        "unit": unit.get("id"),
        "base": base,
        "declared_files": declared_files,
        "changed_files": changed,
    }

    # 1. every change must be inside the unit's declared scope
    mig_dir_for_closed = (plan.get("migration") or {}).get("dir")
    for path in changed:
        if inside_own_range(path, unit, mig_dir_for_closed):
            continue
        hit = matches_any(path, closed)
        if hit and not unit.get("tests_changed"):
            gate.fail(f"changed closed path {path!r} ({hit})")
            continue
        if path not in declared_files:
            gate.fail(f"changed {path!r} which is not in the unit's files")

    # 2. a unit must leave evidence: a change or a new test
    new_tests = [p for p in changed if p.endswith("_test.go") and statuses.get(p) == "A"]
    if not changed:
        gate.fail("no changed files")

    # 3. tests may be added, never edited to pass
    if not unit.get("tests_changed"):
        for path, status in statuses.items():
            if path.endswith("_test.go") and status != "A" and path in declared_files:
                gate.fail(f"modified existing test file {path!r}; add a test instead, or set tests_changed")
    gate.scope["new_tests"] = new_tests

    # 4. no merge to main
    branch = gate.git("rev-parse", "--abbrev-ref", "HEAD").strip()
    gate.scope["branch"] = branch
    if branch in ("main", "master"):
        gate.fail(f"stations run on a unit branch, not {branch!r}")
    if "main" in (unit.get("needs") or []):
        gate.fail("a unit cannot depend on main")

    # 5. migration ids stay in the unit's own range and stay unique
    mig_dir = (plan.get("migration") or {}).get("dir")
    if mig_dir:
        unit_range = unit.get("migration_range")
        if unit_range:
            low, high = int(unit_range[0]), int(unit_range[1])
            for path in changed:
                if not path.startswith(mig_dir.rstrip("/") + "/"):
                    continue
                found = MIGRATION_RE.match(Path(path).name)
                if not found:
                    gate.fail(f"{path!r} does not match NNN_name.sql")
                    continue
                number = int(found.group(1))
                if not (low <= number <= high):
                    gate.fail(f"migration {path!r} id {number} outside this unit's range {unit_range}")
                siblings = [
                    p.name
                    for p in Path(gate.repo, mig_dir).glob("*")
                    if MIGRATION_RE.match(p.name) and int(MIGRATION_RE.match(p.name).group(1)) == number
                ]
                if len(siblings) > 1:
                    gate.fail(f"migration id {number} already used by {sorted(siblings)}")
        elif any(p.startswith(mig_dir.rstrip("/") + "/") for p in changed):
            gate.fail("unit writes a migration but declares no migration_range")

    return gate.scope


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="check.py", description="deterministic per-unit gate")
    parser.add_argument("--unit", required=True)
    parser.add_argument("--plan", default=".graph/plan.json")
    parser.add_argument("--repo", default=".")
    parser.add_argument("--base", default=None, help="base ref; defaults to plan.base or main")
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args(argv)

    repo = Path(args.repo).resolve()
    plan_path = Path(args.plan)
    if not plan_path.is_absolute():
        plan_path = repo / plan_path
    try:
        plan = json.loads(plan_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        print(f"check: cannot read plan {plan_path}: {exc}", file=sys.stderr)
        return 2

    units = {unit.get("id"): unit for unit in plan.get("units") or []}
    unit = units.get(args.unit)
    if not unit:
        print(f"check: unknown unit {args.unit!r}; plan has {sorted(k for k in units if k)}", file=sys.stderr)
        return 2

    base = args.base or plan.get("base") or "main"
    gate = Gate(repo)

    try:
        ignore = tuple(
            dict.fromkeys(
                [
                    plan_path.name,
                    ".graph/plan.json",
                    ".graph/evidence/",
                ]
            )
        )
        scope = evaluate(gate, plan, unit, base, ignore)
    except RuntimeError as exc:
        print(f"check: {exc}", file=sys.stderr)
        return 2

    verdict = "RED"
    evidence = ""
    if not gate.reasons:
        # Build first: never report on tests that cannot compile.
        code, output = gate.run(["go", "build", "./..."])
        if code != 0:
            gate.fail("go build ./... failed")
            evidence = output
        else:
            packages = go_packages(scope.get("changed_files") or [])
            if packages:
                code, output = gate.run(["go", "vet", *packages], timeout=300)
                evidence += output
                if code != 0:
                    gate.fail(f"go vet failed on {packages}")
            if not gate.reasons:
                test_command = plan.get("test_command") or "go test ./..."
                # No package changed (migration, template, CSS): still run the
                # whole suite, because a migration can break any package that
                # boots against the schema.
                target = [*packages] if packages else ["./..."]
                command = test_command.split() + target
                code, output = gate.run(command)
                evidence += output
                if code != 0:
                    gate.fail(f"{' '.join(command)} failed")
        if not gate.reasons:
            verdict = "GREEN"

    tail = [line for line in evidence.splitlines() if line.strip()][-EVIDENCE_LINES:]

    if args.json:
        print(
            json.dumps(
                {
                    "unit": args.unit,
                    "verdict": verdict,
                    "reasons": gate.reasons,
                    "evidence": tail,
                    "scope": scope,
                },
                indent=2,
            )
        )
    else:
        print(f"UNIT      {args.unit}")
        print(f"VERDICT   {verdict}")
        for reason in gate.reasons:
            print(f"REASON    {reason}")
        print(f"SCOPE     {len(scope.get('changed_files') or [])} file(s) changed, "
              f"{len(scope.get('new_tests') or [])} new test file(s): "
              f"{', '.join(scope.get('changed_files') or []) or 'none'}")
        if tail:
            print("EVIDENCE")
            for line in tail:
                print(f"          {line}")

    return 0 if verdict == "GREEN" else 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
