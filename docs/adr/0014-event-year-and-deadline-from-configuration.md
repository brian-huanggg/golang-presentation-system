# ADR 0014: Event year and deadline from configuration

| Item | Value |
|---|---|
| Status | Accepted |
| Date | 2026-11-05 |
| Deciders | Architect, Product Owner |
| Related | PRD EVT-01…03, DDL-001…007, A-06; Architecture 7.1, 8.2, 9.3 |

## Context

The system serves one event year at a time and has one global deadline (PRD Section 5, DDL-001). The Java edition had an event-administration interface, an opening time, and archiving. This edition removes all of that (PRD Section 9). User accounts persist across years, while teams and Ideas belong to one year (EVT-01, EVT-02).

The deadline must be enforced with the system time **when the action completes**, including inside an upload that is in progress (DDL-005), and Admins are exempt (DDL-006).

## Decision

The event year and the deadline are **application configuration**, read once at startup. They are not stored in the database and have no user interface.

- **Settings.** `EVENT_YEAR` (integer, for example `2026`) and `EVENT_DEADLINE` (RFC 3339 timestamp **with an explicit `+08:00` offset**, for example `2026-12-31T23:59:59+08:00`). Names confirmed by the Architect.
- **Fail fast.** Startup fails with a clear message when either value is missing, unparseable, or has no explicit offset. A deadline in the past is allowed, because the system is still useful read-only.
- **One owner.** Package `event` parses and holds the values and exposes `Policy.Check(now)` (Architecture 5.1). All participant write operations call it. Admins bypass it.
- **Injected clock.** `Policy` takes its time from `platform`'s clock interface, so tests control "now" with no sleeps.
- **Checked twice for uploads.** Before storing the object and again inside the committing transaction (Architecture 8.2). The time source is the application server's clock, in UTC internally and displayed in Asia/Taipei (I18N-004).
- **Year columns.** `team.event_year` and `team_member.event_year` record the year of each row. The unique index on `team_member (event_year, user_id)` implements "one team per person per event year" (EVT-02).
- **Changing the deadline** is a configuration change followed by a restart (DDL-007, PRD A-06). The API exposes the current year and deadline read-only to the frontend for display and the countdown (DDL-002).
- **Changing the year** for the next edition is also a configuration change. Old-year rows are left in place and are invisible to participants because every team query filters on the current `event_year`.

## Alternatives considered

- **A `event` table with an Admin interface.** Allows changing the deadline without a restart, but needs an Admin UI, authorization, and audit. All of it is out of scope (ADM-005 and PRD Section 9).
- **Deadline in the database, changed by an engineer with SQL.** Avoids the restart but creates a second place where configuration lives and can silently differ between instances.
- **Re-reading the config file periodically.** Adds a background loop, which contradicts "no scheduler", and makes behavior harder to reason about.
- **Hard-coding the deadline.** Needs a rebuild for every change.

## Consequences

- Extending the deadline requires a restart and, with several instances, a coordinated one. At this scale that is acceptable (PRD A-06).
- If instances are configured differently by mistake, they enforce different deadlines. The read-only API field helps spot this, and a shared configuration source is an operations topic for the DevOps phase (OI-04).
- **Decided:** a deadline change is not audited. It is a deployment-time change, not an application action, and the Product Owner confirmed this on 2026-11-05. The change record lives in the configuration change process.
- The year transition needs no data migration, but a new year starts with zero teams, as intended. Users keep their accounts and must form teams again (EVT-01).
