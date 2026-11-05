# ADR 0003: Feature packages with one-directional dependencies, enforced by `depguard`

| Item | Value |
|---|---|
| Status | Accepted |
| Date | 2026-11-05 |
| Deciders | Architect |
| Related | Architecture 1.3, 5.1–5.3, ADR 0002, ADR 0011 |

## Context

The backend is a modular monolith: one Go module, one binary (Architecture 1.3). Go has no framework that enforces module boundaries, and `internal/` only protects against imports from *outside* the module. Without a mechanical check, coupling creeps in and import cycles appear, which in Go are compile errors that are painful to untangle late.

Two real cross-feature needs exist:

1. `idea` must know whether the caller is the leader or a member of the Idea's team (IDEA-011, IDEA-012).
2. Dissolving a team must soft-delete its Idea in the same transaction (TEAM-015, Architecture 8.5). That is a call from `team` into `idea`, the opposite direction of need 1.

## Decision

Backend application code is organized as **one package per feature** under `backend/internal/`: `platform`, `auth`, `event`, `team`, `idea`, `upload`, `audit`. Imports follow the allow-list below, and `golangci-lint` with `depguard` enforces it. A violation fails the lint check.

### Allowed imports (internal packages only)

| Package | May import |
|---|---|
| `platform` | — (no feature package) |
| `audit`, `upload`, `event` | `platform` |
| `auth` | `platform`, `audit` |
| `team` | `platform`, `auth`, `event`, `audit` |
| `idea` | `platform`, `team`, `event`, `upload`, `audit` |
| `internal/api` (generated) | — |
| `cmd/*` | everything (wiring) |

Every feature package may also import `internal/api` (the generated types and server interface), and `platform` may not. Test files follow the same rules, except that tests may import `platform` test helpers.

### Rules

1. **One direction, no cycles.** The table above is the only allowed set of edges. It matches the diagram in Architecture 5.2.
2. **Interfaces at the consumer.** When a package needs behavior from another feature, it declares the small interface it needs and the other package satisfies it. Wiring happens by manual constructor injection in `cmd/server/main.go`.
3. **No cross-feature repo access.** Handlers and services never call another feature's `repo.go`.

### The `team` → `idea` exception (the reverse call)

`idea` imports `team` (table above). `team` therefore **must not import `idea`**: `depguard` denies it. The cascade on dissolution is expressed with an interface:

```go
// package team — service.go
// IdeaCascade is implemented by package idea and wired in main.go.
type IdeaCascade interface {
    SoftDeleteByTeam(ctx context.Context, tx *sqlx.Tx, teamID int64) error
}
```

`idea.Service` satisfies it, `main.go` passes it into `team.NewService`. The import arrow stays `idea → team`, while the runtime call goes `team → idea` inside the same database transaction. This is the one sanctioned inverse-direction call. Any other reverse call needs a new ADR.

### `depguard` sketch

Illustrative only. The exact keys depend on the `golangci-lint` major version, and `<module>` is the Go module path chosen at scaffolding:

```yaml
linters:
  settings:
    depguard:
      rules:
        team:
          files: ["**/internal/team/**"]
          deny:
            - pkg: "<module>/internal/idea"
              desc: "team must not import idea; use the IdeaCascade interface (ADR 0003)"
        platform:
          files: ["**/internal/platform/**"]
          deny:
            - pkg: "<module>/internal"
              desc: "platform imports no feature package"
```

One rule block per package, generated from the table above, is kept in `backend/.golangci.yml`.

## Alternatives considered

- **Layered by technology (`handlers/`, `services/`, `repositories/`).** Familiar, but every feature touches every layer, so boundaries between features do not exist and cycles arise easily.
- **Separate Go modules or services per feature.** Hard boundaries, but far more moving parts than a ~5,000-team system needs.
- **An event bus for cross-feature reactions.** Removes the reverse call, but audit and soft delete must commit atomically with the change (ADR 0011). A plain function call inside the transaction is simpler.
- **Merge `team` and `idea` into one package.** Avoids the exception entirely. Not chosen because the two have different rules and change rates, and the package would approach half of the domain code.
- **Reviews only, no linter.** Does not hold when the one person who remembers the rules is absent.

## Consequences

- Boundary violations are caught at lint time, not at review time.
- Adding a package or an edge requires editing both this table (via a superseding ADR) and `.golangci.yml`.
- `team`'s tests use a fake `IdeaCascade`, which keeps them independent of `idea`.
- `main.go` grows with wiring code. That is accepted in return for no DI framework.
- Architecture 5.2 and 5.3 point at this ADR and state the exception.
