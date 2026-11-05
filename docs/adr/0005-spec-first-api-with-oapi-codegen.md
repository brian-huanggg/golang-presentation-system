# ADR 0005: Spec-first API with `oapi-codegen`, and a generated frontend client

| Item | Value |
|---|---|
| Status | Accepted |
| Date | 2026-11-05 |
| Deciders | Architect |
| Related | Architecture 4, 5.5, 6, 14 (OI-08), ADR 0002 |

## Context

The frontend and backend are developed against one REST API (`/api/v1`). A contract that is written in code on one side and guessed on the other drifts. The PRD asks for traceable, tested behavior (NFR-QA-001…003), and the Architecture wants "a backend contract change breaks the frontend build" (Architecture 6).

The frontend needs more than plain JSON calls:

- a CSRF header on every state-changing request (AUTH-009),
- `ETag` / `If-Match` on Ideas, with `412` on conflict (IDEA-030),
- RFC 9457 Problem Details with a stable `code` field,
- multipart upload **with progress** (IDEA-007), which `fetch` cannot report.

## Decision

### 1. Contract

`backend/api/openapi.yaml` is hand-written and is the single source of truth. Both sides are generated from it; nobody edits generated files.

- Use **OpenAPI 3.0.x**. `oapi-codegen` does not fully support 3.1 at the time of writing; revisit when it does.
- Every operation has a stable `operationId`, an explicit security requirement (deny by default, Architecture 5.5), and its Problem Details error responses.
- Error `code` values are an enum in the spec.

### 2. Backend

**`oapi-codegen`** generates the Gin server interface and the types into `backend/internal/api/`. Feature handlers implement it. A script (`make api-check` or equivalent) regenerates and fails on any diff. The same script runs in a future pipeline when DevOps is in scope (OI-04).

### 3. Frontend client (resolves OI-08)

**`openapi-typescript` + `openapi-fetch`**:

- `openapi-typescript` generates **types only** (`frontend/src/api/generated/schema.d.ts`). It emits no runtime code, so there is nothing to cover or exclude beyond a declaration file, and a contract change surfaces as a `tsc` error.
- `openapi-fetch` is a very small typed `fetch` wrapper. A middleware adds the CSRF header, and a response handler turns Problem Details into a typed error. `ETag` is read from response headers and sent back as `If-Match`.
- TanStack Query hooks stay hand-written and thin, one set per feature folder, so cache keys and invalidation remain explicit.
- The **upload with progress** is the one hand-written exception: an `XMLHttpRequest` wrapper whose request and response types come from the generated schema.
- MSW handlers in tests are typed with the same generated types.

Exact versions are pinned in `package-lock.json` when the frontend is scaffolded; check at that time that the tools are still maintained and support the chosen OpenAPI version.

## Alternatives considered

- **Code-first (generate the spec from Go handlers).** Gin has no first-class path for this, and the spec would describe what the code does rather than what we agreed.
- **`oapi-codegen` strict-server mode.** More compile-time safety on responses, but more generated glue. Standard mode plus the shared error model is enough here. Revisit if handler boilerplate causes bugs.
- **Frontend: `orval`.** Generates TanStack Query hooks and MSW mocks, which saves typing. Not chosen because it produces a lot of runtime code to exclude from coverage and review, and the CSRF, `If-Match`, Problem Details, and upload-progress needs force a custom mutator anyway.
- **Frontend: `@hey-api/openapi-ts`.** Similar to `orval` with a plugin model. Rejected for the same reasons and a larger surface to keep current.
- **Frontend: `openapi-generator` (`typescript-fetch`).** Requires a Java toolchain and produces verbose classes.
- **Hand-written client and types.** No drift protection.

## Consequences

- A contract change is made in `openapi.yaml` first. Both generators run, and the build fails wherever code no longer matches.
- Generated files are committed so a fresh checkout builds without running the generators; the drift check keeps them honest.
- Frontend API usage stays explicit and slightly more verbose than with a hook generator. We accept this for less generated code and clearer control of the unusual calls.
- Any resource whose behavior the spec cannot express (for example multipart progress) is documented next to the hand-written code, with a link to the operation.
- Architecture 4 (API client row), Architecture 6, and Architecture 14 (OI-08) are updated to match.
