# PRD Review: linear sync multi-team support

## Executive Summary

The PRD is well-intentioned and the fan-out architecture is the right approach. The core
happy path — config resolution, sequential fan-out, per-team timestamps, backward compat —
is sound and buildable. However, five cross-cutting issues were flagged independently by
three or more review legs, indicating high-confidence gaps: push routing is undefined,
the "No storage changes needed" claim contradicts Goal 4, exit codes for partial failure
are unresolved, per-team project filtering is a data-correctness regression for existing
users, and the config key format (Open Question 1) is formally unresolved but all scenarios
assume one answer. **Implementation cannot start safely without resolving the seven
critical questions below.** Once answered, execution risk drops significantly.

---

## Before You Build: Critical Questions

*These questions were flagged by two or more independent review legs. Multi-leg agreement
indicates high-confidence gaps. All seven must be answered before implementation begins.*

### Push Routing

**Q1: What is the precise push routing mechanism for existing issues?**

The PRD says "URL prefix match" routes issues to the correct team on push. Linear issue
URLs are formatted as `https://linear.app/<workspace>/issue/ENG-123` — the team identity
is encoded in the *issue identifier prefix* (`ENG-`), not as a UUID. A literal URL prefix
match distinguishes nothing (all Linear URLs share the same prefix). Without a team
key→UUID mapping (not currently stored in config), there is no way to route a URL to a
configured team UUID.

- Why this matters: two engineers will implement this differently; neither approach is
  specified. Silent mismatch could push issues to the wrong team.
- Found by: ambiguity, feasibility, scope
- Suggested answer options:
  - (a) Parse the issue identifier prefix (`ENG-`), resolve it to a team UUID via an API
    call at startup (one `GET /teams` call, cached in memory for the run)
  - (b) Require users to configure `linear.team_key.<uuid>` manually in config
  - (c) Store the identifier prefix alongside the UUID at config time (e.g., during
    `bd linear sync --setup`)

---

### Storage / Config Model

**Q2: Does "No storage changes needed" mean no new Dolt tables/columns, or literally no new config keys?**

Goal 4 requires independent per-team last-sync timestamps. The current implementation
stores a single `linear.last_sync` key shared across all teams. Implementing Goal 4
requires new keys (`linear.last_sync.<teamUUID>`), which IS a storage change (new rows
in the config table). The Rough Approach's claim "No storage changes needed" contradicts
Goal 4.

- Why this matters: an engineer who reads this claim literally will reuse the shared key
  and silently break incremental sync for all but the last-synced team.
- Found by: ambiguity, feasibility, scope, gaps
- Suggested answer: Clarify that "No storage changes" means "no schema changes" (no new
  columns/tables). New config keys (`linear.last_sync.<teamUUID>`) are required and
  acceptable.

**Q3: Has `linear.team_ids` (comma-separated) been chosen as the config key format?**

Open Question 1 asks comma-sep vs. indexed keys. All six scenarios and the Rough Approach
already assume `linear.team_ids`. The question is formally open but functionally answered
by the rest of the document. Leaving it open creates confusion and the indexed key variant
would require completely different parsing logic.

- Why this matters: the config key format constrains every config read/write path.
- Found by: ambiguity, gaps
- Suggested answer: Close Open Question 1 by specifying `linear.team_ids` (comma-separated).
  Document the UX: `bd config set linear.team_ids "UUID1,UUID2"`.

**Q4: Is "primary team" a formal concept with an explicit config key, or is positional ordering the spec?**

Scenarios 2 and 3 reference the "primary team" as specified behavior. The only definition
appears buried in the Rough Approach: "first in the `team_ids` list." This is informal.
There is no `linear.primary_team_id` config key, no user-visible indicator of which team
is primary, and no way to change primary team without reordering the list.

- Why this matters: users will create issues expecting them in the right team; implicit
  ordering is fragile and invisible.
- Found by: ambiguity, gaps
- Suggested answer options:
  - (a) Positional ordering is the spec; document it explicitly in Goals/Scenarios
  - (b) Add `linear.primary_team_id` as an explicit optional key (falls back to first in
    list if unset)

---

### Error Handling

**Q5: What are the exit code semantics for partial failure?**

Scenario 6 says "warn + continue" but Open Question 4 leaves exit codes unresolved.
This is not a UX preference — it is a correctness question for CI/scripting users.
`bd linear sync && do_something` is silently dangerous if partial failure returns exit 0.

- Why this matters: scripting users will operate on stale data with no indication of
  failure.
- Found by: stakeholders, scope, gaps, ambiguity
- Suggested answer: exit 0 on partial failure (consistent with "warn + continue" semantics)
  with warning to stderr, plus a `--strict` flag for exit non-zero on any error. Document
  this explicitly in the CLI additions section.

---

### Correctness for Existing Users

**Q6: Is per-team `project_id` in or out of scope for v1?**

Users who have `linear.project_id` configured today will have that filter silently applied
to ALL teams when they add a second team. If Team A uses project X and Team B uses project Y,
Team B's issues will be silently excluded. This is a data correctness regression for existing
users, not a new-user gap — and these users are not identified in the PRD's affected-users
section.

- Why this matters: a user adding their first second team could silently lose visibility
  into all of that team's issues, with no error message.
- Found by: feasibility, stakeholders, gaps, scope
- Suggested answer options:
  - (a) Out of scope v1: document that `linear.project_id` applies to all configured teams;
    warn at sync time when multi-team is active and `project_id` is set
  - (b) In scope v1: support `linear.project_id.<team_uuid>` alongside the existing key

---

### Config / CLI Interface

**Q7: How does a user set a multi-value config key via `bd config set`?**

The PRD defines `linear.team_ids` as comma-separated but says nothing about how a user
actually writes it. There is no `bd config append` command. Users who try `bd config set
linear.team_ids UUID2` when `UUID1` is already configured will overwrite the first UUID.

- Why this matters: users will hit this immediately on first use and corrupt their config
  without an error message.
- Found by: gaps
- Suggested answer: Document the explicit UX (`bd config set linear.team_ids "UUID1,UUID2"`).
  Consider adding `bd config append linear.team_ids UUID2` as a convenience command, or
  note this as a known UX gap to address post-launch.

---

## Important But Non-Blocking

*These should be resolved before or during implementation, but need not block start.*

- **`--team` flag scope (pull, push, or both):** Goal 5 says "override sync list for
  this run" (pull+push). Scenario 3 uses it as push-only routing. Implementers will
  choose differently without explicit guidance. (ambiguity, gaps)

- **`validateLinearConfig()` must be updated:** Currently requires `linear.team_id` and
  returns an error if missing. Users configuring only `linear.team_ids` will see a broken
  error message. Trivial to fix but not mentioned in the PRD. (feasibility)

- **`bd linear status` display names vs UUIDs:** Scenario 5 says "display name and
  last-sync timestamp." Display names require an API call per team at status-check time.
  For v1, UUIDs may be acceptable without the extra round-trip. Decide before
  implementing the status command. (scope, gaps, stakeholders)

- **`bd linear teams` scope:** Open Question 5 unresolved. Suggestion from two legs:
  configured teams only for v1 (simpler, no extra API call); discovery via
  `--discover` flag for v2. (scope, gaps, ambiguity)

- **Config migration UX (`team_id` → `team_ids`):** No upgrade path documentation.
  Suggest: print a deprecation notice when both `linear.team_id` and `linear.team_ids`
  are set simultaneously. Add a CHANGELOG entry. (stakeholders, gaps)

- **Backward compat scope definition:** The Constraints section says backward compat
  is "non-negotiable" but doesn't define scope. Does it cover config format only, or
  also command output format and exit codes? Single-team users with scripts parsing
  `bd linear status` output may be affected. (ambiguity, stakeholders)

- **`--team` override (Goal 5) is a v2 candidate:** This is a power-user workflow that
  most users won't need until after they've used multi-team sync for a while. Trimming
  it from v1 reduces CLI surface area and flag validation complexity. (scope)

---

## Observations and Suggestions

- **Rate limit exposure multiplies by team count.** Linear's API rate limits apply
  per-key. Sequential fan-out (already proposed) provides natural throttling. Document
  this implication; no code change needed. (gaps)

- **Parent/child Linear teams may cause duplicate issue pulls.** Issues in a child team
  may appear in the parent; syncing both could pull the same issue twice. Not a blocker
  but worth calling out as a known limitation in the non-goals. (stakeholders)

- **Team removal from config leaves orphaned issues** (issues stay in local DB, don't
  update). Benign for most users but worth documenting as a known limitation. (scope)

- **Error output format for partial failures is unspecified.** Suggest: print to stderr
  as `[warn] team <uuid>: <error>`. Consider persisting last-error per team to config
  so `bd linear status` can surface it. (gaps)

- **Duplicate UUIDs in `team_ids` not validated.** `UUID1,UUID1` would sync the same
  team twice and potentially create duplicate issues on push. Add deduplication at config
  parse time. (gaps)

- **`external_ref` URL format should be documented as a stable contract.** Third-party
  tooling consuming this field relies on its format. The multi-team push routing logic
  now depends on it; making it explicit protects against future breakage. (stakeholders)

- **Fan-out architecture deserves an inline comment block in `linear.go`.** The
  constraint "don't modify Tracker/Client internals" will not be obvious to future
  maintainers. Self-documenting code here reduces maintenance burden. (stakeholders)

---

## Confidence Assessment

| Dimension | Score | Notes |
|-----------|-------|-------|
| Requirements completeness | M | Happy path clear; error handling and edge cases underspecified |
| Technical feasibility | M | Architecture sound; push routing and timestamp storage need design decisions |
| Scope clarity | M | MVP is identifiable; 3 boundary cases need explicit decisions |
| Ambiguity level | M | "URL prefix match" and "No storage changes" are highest-risk contradictions |
| Overall readiness | M | 7 must-answer questions; safely startable once resolved |

---

## Next Steps

- [ ] Human answers the 7 critical questions above
- [ ] PRD updated to reflect decisions (close Open Questions 1, 2, 4; correct "No storage
      changes" claim; specify push routing mechanism; define primary team formality;
      document per-team project_id stance)
- [ ] Pour `design` convoy to generate implementation plan from the updated PRD
