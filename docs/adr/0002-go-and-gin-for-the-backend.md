# ADR 0002: Go and Gin for the backend (exception to the Java standard)

| Item | Value |
|---|---|
| Status | Accepted |
| Date | 2026-11-05 |
| Deciders | Architect; Product Owner (accepted, PRD Q-01 / Architecture OI-01) |
| Related | PRD Q-01, Architecture 1.2, ADR 0003, ADR 0005 |

## Context

The company standard for backend services is Java. The Java edition of this product (`esg-reward`) exists. This edition is a deliberately simplified rewrite (PRD "Derived from"), and the product decision for it is Go with Gin for the backend and a React single-page application for the frontend (Architecture 1.2).

Go and Gin are also in common use in the company. The Product Owner accepted Go with Gin for this system (PRD Q-01).

The workload is small: about 25,000 participants and 5,000 teams, one event per year, files of at most 5 MB, no outbound integrations (Architecture 1, 2). The design goal is "understandable by one person; few moving parts".

Because Go is not the company's default language, the choice is recorded explicitly, so that the reasons remain visible.

## Decision

The backend is written in **Go** with the **Gin** HTTP framework, as a single module and a single binary. The Product Owner accepted this choice (PRD Q-01).

- Go is the latest stable release when the project starts, pinned in `go.mod`. Major versions of Go, Gin, and the other stack components change only through a new ADR (Architecture 4).
- Gin is used as an HTTP router and middleware host only. Business rules do not depend on Gin types (they take `context.Context` and plain structs), so a framework change would touch `handler.go` files and `internal/api/` only.
- Nothing in the architecture relies on Go-only features that Java could not provide. The modular monolith, MariaDB, the OpenAPI contract, and the React frontend would carry over to a Java implementation.

## Alternatives considered

- **Java and Spring Boot (the company standard).** Fully supported by the company, and the Java edition exists. Not chosen because this edition's brief is a small, simple system, and the product decision is Go.
- **Go standard library `net/http` only, or `chi`.** Smaller dependency surface. Not chosen because Gin is part of the product decision and is supported by `oapi-codegen` (ADR 0005). Handlers stay thin, so the choice is cheap to reverse.
- **Other stacks (Node.js/NestJS, Python/FastAPI).** Not considered further: no product or company driver.

## Consequences

- Security tooling and review practices of the Java standard (dependency scanning, coding rules) have no automatic Go equivalent here. This phase does not define them because DevOps is deferred (Architecture 14, OI-04). They must be addressed in the DevOps phase.
- The team uses `gofmt`, `golangci-lint` (with `depguard`, ADR 0003), and `go vet` as the baseline static checks.
