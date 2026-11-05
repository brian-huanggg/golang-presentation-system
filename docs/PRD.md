# ESG Award (Go edition) — Product Requirements Document (PRD)

| Item | Value |
|---|---|
| Document | PRD |
| Version | 1.2 |
| Status | Accepted |
| Owner | Product Owner |
| Last updated | 2026-11-05 |
| Derived from | `esg-reward` PRD v2.0 (Java edition), simplified |
| Related | `docs/Architecture.md`, `backend/api/openapi.yaml` |

> **Scope of this document.** This PRD describes *what* the product does and *why*. It does not prescribe *how* it is built; technical choices are in `docs/Architecture.md`. Requirement IDs of this edition are new. Appendix A maps every removed or changed v2.0 requirement.

---

## Table of Contents

1. [Background and Goals](#1-background-and-goals)
2. [Scope](#2-scope)
3. [Glossary](#3-glossary)
4. [Users and Roles](#4-users-and-roles)
5. [Event and Deadline](#5-event-and-deadline)
6. [Functional Requirements](#6-functional-requirements)
7. [Permission Matrix](#7-permission-matrix)
8. [Non-Functional Requirements](#8-non-functional-requirements)
9. [Out of Scope](#9-out-of-scope)
10. [Assumptions and Accepted Risks](#10-assumptions-and-accepted-risks)
11. [Open Questions](#11-open-questions)
12. [Change Log](#12-change-log)
- [Appendix A — Mapping from v2.0](#appendix-a--mapping-from-v20)

---

## 1. Background and Goals

The company runs an annual **ESG Award** competition. Employees form teams and submit one presentation (an **Idea**) describing their ESG initiative. Today this is handled manually, which makes it hard to enforce eligibility rules (one team per person, team size, department) and the deadline.

### Goals

| ID | Goal | Success measure |
|---|---|---|
| G1 | Let employees register, form teams, and submit an Idea on their own | ≥ 95% of Ideas submitted without engineer help |
| G2 | Enforce eligibility and deadline rules automatically | 0 teams that violate team-size, one-team, or department rules |
| G3 | Provide a trustworthy record of who did what | 100% of audited operations recorded |

### Scale (2026 estimate)

| Metric | Value |
|---|---|
| Teams per event | ~5,000 |
| Participants | ~25,000 |
| Event frequency | Once per year |

No numeric performance targets are defined for this release (see 8.1).

---

## 2. Scope

### In scope

- Self-service registration, login, logout, and password change
- Team creation by a leader who lists the other members by Employee ID; leader adds/removes members; member leaves; leader dissolves
- One Idea per team: title, summary, one PPTX/PDF file, up to 10 versions
- Global deadline enforcement
- Admin: view all teams and Ideas, download any version, and edit Ideas
- Audit log (recorded; no user interface in this release)
- Bilingual UI (Traditional Chinese and English)

### Out of scope

See [Section 9](#9-out-of-scope).

---

## 3. Glossary

| Term | 中文 | Definition |
|---|---|---|
| Employee ID | 工號 | A 6-digit number (digits only) that uniquely identifies an employee. |
| Participant | 參賽者 | A registered, non-admin user. |
| Team Leader | 組長 | The participant who created a team. Exactly one per team. |
| Team Member | 組員 | A participant in a team who is not the leader. |
| Track | 報名類型 | Smart Innovation (智慧創新) or Sustainable Development (永續發展). |
| Award Category | 獎項 | Engineering (工程), Administration (行政), Manufacturing Process (製程), Transportation (運輸), Public Safety (公安). |
| Department | 部門 | TSID, BCID, DEID, or NTAD. |
| Idea | 提案 | The single entry of a team: a title, a summary, and a file with its versions. |
| Version | 版本 | A stored revision of the Idea file. |
| Event Year | 活動年度 | One annual edition of the competition. |
| Deadline | 截止時間 | The single cut-off time of the event year (Asia/Taipei). |
| Admin | 管理員 | Organizer account provisioned from Vault. Cannot participate in a team. |
| Soft delete | 軟刪除 | Data is hidden immediately and permanently removed after 7 days. |

---

## 4. Users and Roles

| Role | Description | How the role is obtained |
|---|---|---|
| Visitor | Not logged in | — |
| Participant | Registered employee without a team, or a Team Member | Self-registration |
| Team Leader | Participant who created a team | Creating a team |
| Admin | Organizer | Provisioned from Vault only |

### Role rules

- **R-01** A person (Employee ID) has exactly one role: Participant or Admin.
- **R-02** Admins cannot be members or leaders of any team.
- **R-03** Admin accounts can be created **only** through the secure configuration store (Vault). The application offers no way to create, promote, demote, or delete an Admin.
- **R-04** An Employee ID that belongs to an Admin can never be used as a Participant.

---

## 5. Event and Deadline

The system serves one event year at a time. The event year and its deadline come from application configuration, not from a user interface.

| State | Meaning | Participants can |
|---|---|---|
| Open | Current time is before the deadline | Register, form teams, submit, log in, view |
| Closed | Current time has reached the deadline | Log in, view their team and Idea, download versions |

- **EVT-01** User accounts persist across event years. Teams and Ideas belong to one event year.
- **EVT-02** The one-team-per-person rule applies per event year.
- **EVT-03** There is no opening time: registration is possible from go-live until the deadline.

---

## 6. Functional Requirements

Priority: **M** = Must, **S** = Should, **C** = Could.

### 6.1 Registration and Account (REG)

| ID | Requirement | Priority |
|---|---|---|
| REG-001 | A visitor can register with: Employee ID, full name, department, password, preferred language, and an optional email. | M |
| REG-002 | Employee ID must be exactly 6 digits (0–9). Letters, symbols, and spaces are rejected. | M |
| REG-003 | An Employee ID can be registered only once. | M |
| REG-004 | Department is chosen at registration from TSID, BCID, DEID, NTAD. Nobody can change it through the application. | M |
| REG-005 | **Password policy:** at least 12 characters; at least one uppercase letter, one lowercase letter, and one symbol; no sequence of 3 or more consecutive letters or digits (e.g. `abc`, `cba`, `123`, `321`); no character repeated 3 or more times in a row (e.g. `aaa`, `111`). Enforced on registration and password change. | M |
| REG-006 | Registration is not possible after the deadline. | M |
| REG-007 | A logged-in user can change their password (current password required) and preferred language. | M |
| REG-008 | The email is stored if provided, format-checked only. It is not verified, not unique, not used for login, and not used to send messages in this release. | M |
| REG-009 | Registration responses may reveal that an Employee ID is already registered (accepted trade-off: there is no email channel to inform the owner). Login responses never reveal this (AUTH-003). | M |

**Acceptance criteria (examples)**

- **AC-REG-003** *Given* Employee ID `123456` is registered, *when* another visitor registers with `123456`, *then* registration fails and no second account exists.
- **AC-REG-005** *Given* the password `Abcdef!Xyz12Q`, *when* submitted, *then* it is rejected because it contains `bcd` / `xyz`.

### 6.2 Authentication and Session (AUTH)

| ID | Requirement | Priority |
|---|---|---|
| AUTH-001 | Users log in with **Employee ID + password**. Email is never a login identifier. | M |
| AUTH-002 | After **5** consecutive failed attempts, the account is locked for **5 minutes**. | M |
| AUTH-003 | Login failure messages are always generic ("Invalid Employee ID or password"). They do not reveal whether the account exists or is locked. | M |
| AUTH-004 | Repeated login attempts from the same network source are rate-limited, independent of the per-account lock. | M |
| AUTH-005 | A session expires after **30 minutes** of inactivity and **8 hours** in total, whichever comes first. | M |
| AUTH-006 | A user can log out; the session is invalidated immediately. | M |
| AUTH-007 | After a password change, all other sessions of that user are invalidated. | M |
| AUTH-008 | Admin accounts provisioned from Vault must change their password at first login. | M |
| AUTH-009 | The application is protected against cross-site request forgery for every state-changing action. | M |
| AUTH-010 | There is **no self-service password recovery**. A forgotten password is reset by an engineer directly in the database (see Section 10). | M |

### 6.3 Teams (TEAM)

#### Creation

| ID | Requirement | Priority |
|---|---|---|
| TEAM-001 | A Participant who is not in a team can create a team while the event is Open and becomes its Team Leader. | M |
| TEAM-002 | When creating a team, the leader provides: team name, Track, Award Category, and the **Employee IDs of the other members** (0 to 4 IDs). | M |
| TEAM-003 | The team's department is the leader's department. | M |
| TEAM-004 | Team name is required, 2–50 characters, unique within the event year (case-insensitive) among active teams. | M |
| TEAM-005 | Track, Award Category, and Department cannot be changed after creation. (To correct them, the leader dissolves the team and creates a new one.) | M |
| TEAM-006 | A team has 1 to 5 people, including the leader. | M |
| TEAM-007 | **Member validation.** Every listed Employee ID must: belong to a registered Participant; not be the leader; appear only once in the list; not already be in a team in this event year; have the same department as the leader. | M |
| TEAM-008 | Team creation is all-or-nothing. If any listed ID is invalid, no team is created and the response states, for each invalid ID, the reason. | M |

#### Changing the roster

| ID | Requirement | Priority |
|---|---|---|
| TEAM-010 | The leader can add a member by Employee ID while the event is Open. The same validation as TEAM-007 and the size limit of TEAM-006 apply. | M |
| TEAM-011 | The leader can remove a member while the event is Open (with confirmation). | M |
| TEAM-012 | A Team Member can leave the team while the event is Open (with confirmation). | M |
| TEAM-013 | The leader cannot leave. They must dissolve the team. | M |
| TEAM-014 | The leader can dissolve the team while the event is Open, after explicit confirmation. | M |
| TEAM-015 | Dissolution is a **soft delete**: the team, its Idea, and all versions are hidden immediately and permanently removed after **7 days**. Members are released immediately and the team name is released immediately. | M |
| TEAM-016 | Restoring a dissolved team is not provided in this release. The audit record of the dissolution lists the former roster so an engineer can restore it manually within 7 days. | M |
| TEAM-017 | After the deadline, all team changes are blocked (see DDL-003). | M |

**Acceptance criteria (examples)**

- **AC-TEAM-007** *Given* leader A (department TSID) and a listed ID of a BCID participant, *when* A creates the team, *then* no team is created and the response states a department mismatch for that ID.
- **AC-TEAM-015** *Given* team T is dissolved, *when* a former member creates a new team, *then* creation succeeds.

### 6.4 Ideas and Files (IDEA)

#### File rules

| ID | Requirement | Priority |
|---|---|---|
| IDEA-001 | Each team has at most one Idea (one main file). | M |
| IDEA-002 | Allowed formats: **PPTX** and **PDF** only. Macro-enabled or other formats (`.pptm`, `.ppt`, `.key`, …) are rejected. | M |
| IDEA-003 | Maximum file size: **5 MB**. | M |
| IDEA-004 | A file is accepted only if its extension, declared type, and actual content all match an allowed format. | M |
| IDEA-005 | Every file is scanned for malware before it becomes visible to anyone. Infected files are rejected with a "security" reason. | M |
| IDEA-006 | An interrupted or abandoned upload must not create a visible version or leave the Idea inconsistent. | M |
| IDEA-007 | The user sees upload progress and a clear result: accepted, or rejected with the reason (format, size, security, deadline, conflict). | M |

#### Content and permissions

| ID | Requirement | Priority |
|---|---|---|
| IDEA-010 | An Idea consists of the file, a title (required, max 100 characters), and a summary (optional, max 1,000 characters). | M |
| IDEA-011 | **Only the Team Leader and Admins** can upload the first file, replace the file, edit title and summary, and delete the Idea. | M |
| IDEA-012 | Team Members can view the Idea and its versions and download any version, but cannot change anything. | M |
| IDEA-013 | Online preview and editing are not provided. Files are downloaded individually. | M |

#### Versions

| ID | Requirement | Priority |
|---|---|---|
| IDEA-020 | Every successful upload or replacement creates a new version recording: version number, uploader, upload time, original file name, size. | M |
| IDEA-021 | Up to **10** versions are kept. When an 11th is created, the oldest is permanently removed. | M |
| IDEA-022 | Team members and Admins can view the version list and download any kept version. | M |
| IDEA-023 | Nobody can delete an individual version. | M |
| IDEA-024 | The latest version is the official entry. | M |

#### Concurrent edits and deletion

| ID | Requirement | Priority |
|---|---|---|
| IDEA-030 | If the Idea changed after a user opened it (for example the leader and an Admin edit at the same time), the second save is rejected, the user is told someone else updated it, and the latest state is shown. No change is silently overwritten. | M |
| IDEA-040 | Deleting an Idea (with confirmation) is a **soft delete**: the Idea and all versions are hidden immediately and permanently removed after **7 days**. | M |
| IDEA-041 | Restoring a deleted Idea is not provided in this release (engineer procedure within 7 days). | M |
| IDEA-042 | After deletion, a new Idea can be uploaded, starting at version 1. | M |

**Acceptance criteria (examples)**

- **AC-IDEA-003** *Given* a 5.1 MB PDF, *when* the leader uploads it, *then* it is rejected with a size message and no new version exists.
- **AC-IDEA-011** *Given* a Team Member, *when* they open the Idea page, *then* no upload, edit, or delete action is available.
- **AC-IDEA-030** *Given* the leader and an Admin both open the Idea at version 3, *when* the Admin saves (version 4) and then the leader saves, *then* the leader's save is rejected and shows version 4.

### 6.5 Deadline (DDL)

| ID | Requirement | Priority |
|---|---|---|
| DDL-001 | The event year has one global deadline, expressed in Asia/Taipei time, set in application configuration. | M |
| DDL-002 | The deadline is displayed on every participant page, with a countdown during the last 7 days. | S |
| DDL-003 | After the deadline, participants **cannot**: register, create a team, add/remove/leave members, dissolve a team, upload/replace/delete files, or edit title/summary. | M |
| DDL-004 | After the deadline, participants **can** still log in, change their password, view their team and Idea, and download versions. | M |
| DDL-005 | The deadline is enforced using the system time at the moment the action completes, not when the page was opened. An upload in progress at the deadline is rejected if it is not completed before it. | M |
| DDL-006 | Admins are not restricted by the deadline. | M |
| DDL-007 | Extending the deadline is a configuration change followed by a restart of the application. | M |

### 6.6 Administration (ADM)

| ID | Requirement | Priority |
|---|---|---|
| ADM-001 | Admins can list and search teams by name, Track, Award Category, department, and whether an Idea exists. | M |
| ADM-002 | Admins can view any team's members, Idea, and versions, and download any version. | M |
| ADM-003 | Admins can upload the first file, replace the file, edit title and summary, and delete the Idea of any team, at any time. | M |
| ADM-004 | **Provisioning.** At startup, the application creates Admin accounts listed in the Vault-provided configuration (Employee ID, full name, email, password hash) when they do not yet exist. Existing accounts are never overwritten. If a listed Employee ID already exists as a Participant, startup fails with a clear error. | M |
| ADM-005 | Admin management of users, accounts, team rosters, and team attributes is **not** provided in this release. | M |

### 6.7 Audit Log (AUD)

| ID | Requirement | Priority |
|---|---|---|
| AUD-001 | The following are recorded: registration, login success/failure, logout, lockout, password change, every team action (create, add/remove/leave member, dissolve), every Idea action (upload, replace, metadata edit, delete), every file download, every Admin view of another team, every Admin action, Admin provisioning. | M |
| AUD-002 | Each record contains: timestamp (Asia/Taipei), actor Employee ID and role, action, target (user/team/idea/version), result (success/failure), source network address, and before/after values for changes. | M |
| AUD-003 | Audit records are append-only. Nobody can edit or delete them through the application. | M |
| AUD-004 | Audit records are kept for at least **1 year**. The application never purges them. | M |
| AUD-005 | No user interface or API for reading the audit log is provided in this release. Engineers query the database. | M |
| AUD-006 | Audit records never contain passwords, session identifiers, or file contents. | M |
| AUD-007 | Soft-delete purges keep the audit records of the purged data. | M |

### 6.8 Localization (I18N)

| ID | Requirement | Priority |
|---|---|---|
| I18N-001 | The UI is available in **Traditional Chinese (zh-TW)** and **English (en)**. Users can switch at any time. | M |
| I18N-002 | Preferred language is saved to the user account and applied on every login. Before login, the browser's last choice is used. | M |
| I18N-003 | **Error messages are in English only**, in both UI languages. | M |
| I18N-004 | Dates and times are displayed in Asia/Taipei time with the time zone shown. | M |
| I18N-005 | Tracks and award categories are shown in the user's UI language; department codes are shown as-is. | M |

---

## 7. Permission Matrix

✅ = allowed · ❌ = not allowed · ⏱ = only while the event is Open

| Action | Visitor | Participant (no team) | Team Member | Team Leader | Admin |
|---|---|---|---|---|---|
| Register | ⏱ | — | — | — | — |
| Log in / change own password | ✅ login | ✅ | ✅ | ✅ | ✅ |
| Create team | ❌ | ⏱ | ❌ | ❌ | ❌ |
| Add / remove member | ❌ | ❌ | ❌ | ⏱ | ❌ |
| Leave team | ❌ | — | ⏱ | ❌ (dissolve) | — |
| Dissolve team | ❌ | ❌ | ❌ | ⏱ | ❌ |
| Upload / replace file, edit title & summary, delete Idea | ❌ | ❌ | ❌ | ⏱ | ✅ |
| View / download own team's Idea | ❌ | ❌ | ✅ | ✅ | ✅ |
| View / search / download any team | ❌ | ❌ | ❌ | ❌ | ✅ |

---

## 8. Non-Functional Requirements

This phase covers the business functions of the frontend and backend only. DevOps and operations are out of scope (Section 9).

### 8.1 Performance and capacity

| ID | Requirement |
|---|---|
| NFR-PF-001 | **No numeric performance targets and no load test are required in this release.** Expected load (~25,000 participants) is small. |

### 8.2 Security

| ID | Requirement |
|---|---|
| NFR-SEC-001 | No secrets (passwords, keys, tokens) in source code. |
| NFR-SEC-003 | Passwords are stored only with **Argon2id** (salted, one-way). |
| NFR-SEC-005 | Files are only accessible to authorized users and are always served as downloads. |

### 8.3 Quality

| ID | Requirement |
|---|---|
| NFR-QA-001 | Backend and frontend automated test **statement coverage ≥ 80%**. Exclusions are limited to generated code and the program entry points. |
| NFR-QA-002 | End-to-end tests cover every "Must" requirement in Section 6 that has a user-visible flow. |
| NFR-QA-003 | Every "Must" requirement is traceable to at least one automated test (requirement ID in the test name). |

### 8.4 Compatibility and usability

| ID | Requirement |
|---|---|
| NFR-UX-001 | Supported browser: **Google Chrome**, latest two major versions, desktop and mobile. |
| NFR-UX-002 | Responsive layout from 360 px width to desktop. |
| NFR-UX-003 | Destructive actions (dissolve team, delete Idea, remove member, leave team) require explicit confirmation. |

---

## 9. Out of Scope

| Item | Note |
|---|---|
| **DevOps and operations** | Deployment, containers, Kubernetes, CI/CD pipelines, security-scan gates, environments, monitoring, dashboards, alerting, backup and recovery, availability SLO / RPO / RTO, TLS, secrets infrastructure, operations runbook, deployment / user / admin manuals. Planned for a later phase. |
| Email sending and email verification | The email field is stored only (REG-008). |
| Forgot / reset password flows | AUTH-010: engineer procedure. |
| Admin identity and account management (release Employee ID, unlock, change department, create Admin in UI) | ADM-005. Admin creation only via Vault. |
| Admin editing of team rosters or attributes | Leader dissolves and recreates. |
| Restoring soft-deleted data through the application | Engineer procedure within 7 days. |
| Entry list CSV export | Removed. |
| Audit log interface and CSV export | Records are written (Section 6.7). |
| Reminder emails, notifications | — |
| Scheduled background jobs | Maintenance is done by manual command-line tools. |
| Event administration interface, opening time, archiving | Deadline comes from configuration. |
| Judging, scoring, award announcement | — |
| SSO, OTP / two-factor authentication, HR directory integration | Later phase. |
| Online preview or editing; real-time co-editing; multiple files; ZIP download | — |
| Browsers other than Chrome; public API | — |
| Participant account deletion | — |
| Performance targets and load testing | NFR-PF-001. |

---

## 10. Assumptions and Accepted Risks

| ID | Item |
|---|---|
| A-01 | Employee IDs are not reused by the company for different people. |
| A-02 | Department codes (TSID, BCID, DEID, NTAD) are fixed for the event year. |
| A-03 | Without email verification or an HR list, **anyone can register an unused Employee ID**. Accepted risk. |
| A-04 | A participant who forgot their password, or whose Employee ID was registered by someone else, can only be helped by an engineer editing the database. Accepted; handled directly by an engineer. |
| A-05 | A leader can place any registered, eligible participant in the team without their prior consent; the participant can leave (TEAM-012). Accepted. |
| A-06 | Extending the deadline requires a configuration change and a restart. Accepted. |

---

## 11. Open Questions

| ID | Question | Owner | Proposed default |
|---|---|---|---|
| Q-01 | Is Go (with Gin) accepted as the backend technology? | Product Owner | **Resolved: accepted.** Go and Gin are also in common use in the company. See ADR 0002. |
| Q-02 | Retention of audit records beyond 1 year? | Product Owner | **Resolved: 1 year** (AUD-004). |

---

## 12. Change Log

| Version | Date | Author | Changes |
|---|---|---|---|
| 1.2 | 2026-11-05 | Product Owner | Accepted. Resolved Q-01 (Go and Gin accepted) and Q-02 (audit retention 1 year). Removed NFR-PF-002. Corrected the cross-reference in Section 6.1 (performance is in 8.1). |
| 1.1 | 2026-11-05 | Product Owner | Removed all DevOps / operations content (availability, backup, observability, environments, release gates, deployment manuals); business functions only. |
| 1.0 | 2026-11-05 | Product Owner | New edition derived from `esg-reward` PRD v2.0: removed email flows, join-by-code, admin identity management, CSV export, event administration, scheduled jobs, performance targets; added roster-by-Employee-ID team creation and leader/Admin-only Idea editing. |

---

## Appendix A — Mapping from v2.0

| v2.0 requirement(s) | Status in this edition |
|---|---|
| REG-001/002/003/006/007/008 | Kept (REG-001…005). Email becomes optional. |
| REG-004, REG-005 (domain, unique email) | Removed. Replaced by REG-008. |
| REG-009, REG-010 (email verification) | Removed. |
| REG-011 (before opening time) | Reduced to REG-006 (after deadline only). |
| REG-012 (no existence disclosure on registration) | Reversed: REG-009. |
| REG-013 | Kept as REG-007. |
| AUTH-001…006, 009, 011 | Kept (AUTH-001…007, 009). |
| AUTH-007, AUTH-008 (forgot/reset password) | Removed. AUTH-010. |
| AUTH-010 (one-time password for Super Admin) | Kept as AUTH-008 for all Admins. |
| R-03…R-08 (Super Admin, promotion, break-glass) | Removed. R-03 (Vault only). |
| TEAM-010…014 (team code, invite link, join) | Removed. Replaced by TEAM-007/008/010. |
| TEAM-020, 021, 023, 024, 025, 026 | Kept (TEAM-011…015). |
| TEAM-022 (transfer leadership) | Removed. |
| TEAM-027 (restore team) | Removed. TEAM-016. |
| TEAM-028 (notification email) | Removed. |
| SUB-* | Renamed IDEA-*. SUB-012 changed: members are read-only (IDEA-012). SUB-041 removed. |
| DDL-001…007 | Kept (opening time removed; DDL-007 now configuration). |
| DDL-008…010 (reminder emails) | Removed. |
| ADM-001, 002, 003 | Kept, narrowed to Ideas (ADM-001…003). |
| ADM-004…009, 011…014 | Removed (rosters, attributes, restore, unlock, release ID, export, create Admin). ADM-010 → configuration. |
| AUD-001…003, 006, 007 | Kept. AUD-004 without the second-copy requirement. AUD-005 reduced to "no UI". |
| I18N-004 (bilingual email) | Removed. |
| NFR-PF-001 | Kept (states no performance targets). |
| NFR-PF-002 | Removed on 2026-11-05 (not a requirement of this release). |
| NFR-QA-001 | Branch → statement coverage. |
| NFR-AV-*, NFR-OB-*, NFR-ENV-*, NFR-DOC-001, NFR-SEC-002, NFR-SEC-004, NFR-QA-004 | Removed from this phase (DevOps / operations). |
| Event states NotOpen / Archived | Removed. |
