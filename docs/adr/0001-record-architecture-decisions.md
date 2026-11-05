# ADR 0001: Record architecture decisions

| Item | Value |
|---|---|
| Status | Accepted |
| Date | 2026-11-05 |
| Deciders | Architect; Product Owner (accepted) |
| Related | `docs/Architecture.md` Section 12 |

## Context

The Architecture document says what the structure is. It does not keep the reasons behind each choice, and reasons are what get lost: a year from now (the event is annual) nobody will remember why sessions live in MariaDB or why there is no scheduler. The project is maintained by very few people, so the record has to be cheap to write and cheap to read.

## Decision

We record every significant architecture decision as an Architecture Decision Record (ADR), following Michael Nygard's lightweight format.

- **Location.** `docs/adr/`, one Markdown file per decision.
- **Naming.** `NNNN-short-title.md`, four digits, numbered sequentially, never reused. The list of planned ADRs is in `docs/Architecture.md` Section 12.
- **Granularity.** One decision per ADR. "Significant" means: hard to reverse, affects more than one feature package, changes a major version in the stack, or relaxes a stated constraint.
- **Sections.** Context, Decision, Alternatives considered, Consequences. A header table holds Status, Date, Deciders, and Related.
- **Statuses.** `Proposed` → `Accepted` → optionally `Superseded by NNNN` or `Deprecated`.
- **Immutability.** After `Accepted`, the text is not edited, except for the Status line and fixing broken links. To change a decision, write a new ADR and mark the old one `Superseded by NNNN`.
- **Where facts live.** The ADR holds the *why*. `Architecture.md` holds the resulting *structure* and links to the ADR. If the two disagree, fix `Architecture.md`, or write a superseding ADR if the decision itself changed.
- **Language.** English, like the other documents.

### Template

```markdown
# ADR NNNN: <title>

| Item | Value |
|---|---|
| Status | Proposed / Accepted / Superseded by NNNN |
| Date | YYYY-MM-DD |
| Deciders | <roles> |
| Related | <PRD IDs, other ADRs> |

## Context
<Forces at play, constraints, PRD requirements. Facts, not opinions.>

## Decision
<What we do, in full sentences, active voice.>

## Alternatives considered
<Each option and the reason it was not chosen.>

## Consequences
<What becomes easier, what becomes harder, what we must watch, follow-up work.>
```

## Alternatives considered

- **Only `Architecture.md`, no ADRs.** Simplest, but the document is rewritten as the design evolves, so the history of *why* disappears.
- **Wiki or ticket comments.** Separate from the code, easily drifts, not reviewed with the change.
- **Heavier formats (MADR with option scoring, RFCs).** More ceremony than a one-person-sized project needs.

## Consequences

- Each significant decision costs about one page of writing.
- Reviewers can ask "where is the ADR?" for any change that touches the structure.
