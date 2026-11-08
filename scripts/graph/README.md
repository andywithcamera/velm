# Per-unit execution graph

Two files, no dependencies beyond Python 3 and Go:

- `graph.py` — validates a plan and schedules its units into dependency waves.
- `check.py` — the gate. A unit claims GREEN by exiting 0; nothing else counts.

A **unit** is one station's whole job: a task a test can prove, the files it
may touch, the files it reads, and the units it needs.

## The plan

```json
{
  "goal": "one sentence, the whole effort",
  "base": "main",
  "test_command": "go test",
  "closed_paths": ["go.mod", "go.sum", ".graph/**", "scripts/graph/**", ".github/**", "internal/db/migrations/**"],
  "migration": { "dir": "internal/db/migrations", "ranges": [[100, 139]] },
  "units": [
    {
      "id": "U0",
      "kind": "interface",
      "task": "Extract the summary type so both call sites share its shape.",
      "files": ["internal/authz/GrantPath.go"],
      "reads": [],
      "needs": [],
      "migration_range": [100, 109]
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `kind` | `interface` lands first (shared shapes, stubs, signatures); `work` fills it in. |
| `files` | Files the unit may write. Anything else is RED. |
| `reads` | Files it may read but not write. |
| `needs` | Units that must be GREEN before this one starts. |
| `tests_changed` | Set only when a unit's job is to change an existing test. Otherwise editing a `_test.go` is RED. |
| `migration_range` | Ids this unit may claim. Ranges must not overlap. |

`closed_paths` are off-limits to every unit: the lockstep files and the gate
itself. The exception is a migration inside a unit's own reserved range.

## Validate and schedule

```bash
python3 scripts/graph/graph.py validate --plan .graph/plan.json
python3 scripts/graph/graph.py waves --plan .graph/plan.json
```

`validate` exits 1 on any error. It catches what costs a wave when it is found
late:

- two units writing the same file
- an unmet `needs`, a self-need, a cycle, or a dependency on `main`
- reads that are never produced by any unit
- a file in both `files` and `reads`
- a unit that writes a closed path
- a migration id outside its unit's range, an id already used in the repo, or
  two units whose ranges overlap
- a task that is not a sentence a test could prove

`waves` prints the order: a unit is `GATED eligible` or `held for deps`, with
its deps listed. Unblocked units run in parallel.

## The gate

```bash
python3 scripts/graph/check.py --unit U0                # human readable
python3 scripts/graph/check.py --unit U0 --json         # machine readable
```

```
UNIT      U0
VERDICT   GREEN
SCOPE     2 file(s) changed, 1 new test file(s): internal/authz/GrantPath.go, internal/authz/GrantPath_test.go
```

Exit codes: `0` GREEN, `1` RED, `2` the gate could not run.

RED prints `REASON` lines and `EVIDENCE`, the tail of the actual command
output. A RED unit is re-dispatched alone, never as a batch, with its reasons
as the brief. That is the point: the failure is localised, so the retry is too.

What the gate decides:

| Check | Rule |
| --- | --- |
| Scope | Every changed file is in the unit's `files`, and not a closed path. |
| Evidence | Something changed. |
| Tests | Only `_test.go` files may be added freely; modifying an existing one is RED unless `tests_changed`. |
| Branch | Never `main`. |
| Migrations | Id inside the unit's range, and not already used in the repo. |
| Build | `go build ./...`. |
| Vet | `go vet` on the changed packages. |
| Tests | `test_command` on the changed packages, or `./...` if no package changed. |

## What the gate cannot do

It scores the artefact, not the claim, and not the intent. A unit that writes
a weak test which passes is GREEN: a human reviewing the diff judges shape.
Not useful here, judged useless.

What is worth building is the opposite detector: **test hiding**. If a unit
edits a test to make it pass, the gate catches it today because editing an
existing test is RED unless the unit declares `tests_changed`. A green suite
that stopped exercising anything while `tests_changed` is set is still
reviewable by eye, and by the suite's own count.

## Cost, measured on this repo

| Fact | Value |
| --- | --- |
| Tests | 426 in 92 files, 8 packages |
| `go test ./...` cold | 35.2 s (compile included) |
| `go test ./...` warm | ~1.0 s total |
| Postgres needed | No. Integration tests skip without `TEST_DATABASE_URL`. |

The gate is cheap enough to run per unit, which is the whole reason it can be
the authority instead of a model's opinion.
