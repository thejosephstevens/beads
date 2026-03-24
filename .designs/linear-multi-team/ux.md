# User Experience Analysis

## Summary

The `bd linear sync` multi-team feature fundamentally changes the user's mental model of
sync: from "one config → one team → sync" to "one config → N teams → fan-out." The good
news is that this is a natural mental model extension users will intuit quickly. The
challenge is in the details: setup UX (how do you add a second team?), primary-team
semantics (where does a new issue go?), partial failure feedback (one of N teams failed),
and `bd linear status` readability (one row of data becomes N rows).

This analysis identifies five UX areas requiring deliberate decisions, proposes a
recommended approach for each, and flags three questions that need human input before
implementation.

---

## Analysis

### Key Considerations

- **Backward compatibility is the UX foundation.** Single-team users must see no
  disruption. Any UX change that affects single-team users undermines trust.
- **Setup is the highest-friction moment.** Getting `linear.team_ids` right (UUIDs,
  comma-separated, the right teams) is where users will first fail and be confused.
- **The `bd linear teams` discovery command is load-bearing.** Without an easy way to
  look up UUIDs, users will abandon setup or configure incorrectly.
- **`bd linear status` is the primary feedback loop.** Users check this to understand
  what's happening; it must scale to N teams readably.
- **Partial failure is the defining error case.** Sync touching multiple external APIs
  will fail partially. Silent partial failure (exit 0, no feedback) erodes trust quickly.
- **Primary team is invisible until it matters.** When a user runs `bd linear sync --push`
  and a new issue goes to the wrong team, they'll be confused and frustrated. This needs
  to be surfaced clearly, not buried in docs.

---

### Options Explored

#### Option 1: Implicit primary team (positional ordering)

- **Description**: First UUID in `linear.team_ids` is the primary team. No explicit config
  key. Documented in `--help` and `bd linear status`.
- **Pros**: Simple, no new config key, consistent with "first wins" conventions in other
  tools.
- **Cons**: Invisible. A user who reorders their team list to "alphabetize" it silently
  changes which team gets new issues. Status output should reinforce this with a `(primary)`
  label; without it, users won't know.
- **Effort**: Low

#### Option 2: Explicit `linear.primary_team_id` config key

- **Description**: Add `linear.primary_team_id` as an optional config key. Falls back to
  first-in-list if unset.
- **Pros**: Explicit, auditable, survives reordering. `bd linear status` can display it
  unambiguously.
- **Cons**: Two ways to configure primary team creates support confusion. New users
  learning multi-team config now have one more key to understand.
- **Effort**: Low-Medium (config key + validation + status display)

#### Option 3: Setup wizard (`bd linear sync --setup`)

- **Description**: Interactive setup flow. Fetches available teams, lets user select
  primary, configures `linear.team_ids` and optionally `linear.primary_team_id`.
- **Pros**: Best onboarding experience. Handles UUID discovery inline. Hard to misconfigure.
- **Cons**: Highest implementation cost. Interactive flows are hard to script. Out of scope
  for v1 if setup is already manual.
- **Effort**: High

#### Option 4: Partial failure — warn-and-continue with `--strict` flag

- **Description**: Default behavior: if one team fails, log a warning to stderr and continue
  with remaining teams. Exit code 0. With `--strict`, any team failure exits non-zero.
- **Pros**: Matches user expectation for resilience (one bad team shouldn't abort everything).
  CI users who need hard failure get `--strict`. Consistent with other multi-target tools.
- **Cons**: Users without `--strict` in scripts may silently lose partial data.
- **Effort**: Low (flag addition + stderr output)

#### Option 5: Partial failure — always exit non-zero on any team error

- **Description**: Any team failure = non-zero exit. No `--strict` flag needed.
- **Pros**: Safer for scripting, more predictable.
- **Cons**: Overly strict for interactive use. One transient API error for team B aborts
  push to team A as well. Users lose idempotency benefits.
- **Effort**: Low

---

### Recommendation

**Primary team: Option 1 (positional) + `(primary)` label in status output.**

Explicit config adds complexity without proportionate value for v1. The `bd linear status`
display must label the first team as `(primary)` so users know the convention is in effect.
Add a short note to `bd linear sync --help`:

```
Note: When pushing new issues, the first team in linear.team_ids is the primary (target)
team. To change primary team, reorder the list.
```

**Partial failure: Option 4 (warn-and-continue + `--strict`).**

This is the right default. It matches the PRD's stated "warn + continue" semantics and is
consistent with tools like `make`, `rsync`, and multi-target deploy scripts. Explicitly
document the exit code behavior:

```
Exit codes:
  0    All teams synced successfully (or partial failure without --strict)
  1    At least one team failed (with --strict or fatal error)
```

**Config key format: comma-separated `linear.team_ids` (close the open question).**

The functional choice is already made by the rest of the PRD. Confirm it. Document the
full setup sequence prominently in `bd linear sync --help`:

```bash
# Setup (run bd linear teams to find UUIDs):
bd config set linear.api_key "YOUR_API_KEY"
bd config set linear.team_ids "UUID1,UUID2"

# Single team (backward compat):
bd config set linear.team_id "UUID1"   # still works
```

---

## Constraints Identified

1. **`bd config set` is additive-overwrite only.** There is no `bd config append`. Users
   who add a second team must know to include both UUIDs in the same command
   (`bd config set linear.team_ids "UUID1,UUID2"`), not to call `set` twice. A single
   misstep overwrites their config silently. This is a hard constraint on the setup UX —
   the docs and `--help` must make this explicit with a concrete example.

2. **`bd linear status` is single-team today.** The current output format is flat
   (one `Team ID:`, one `Last Sync:`). Extending to N teams changes the visual structure.
   The new output should group by team, not flatten across teams.

3. **UUID-only config degrades discoverability.** Users configure UUIDs but see them as
   opaque strings in config and status output. Display names require an API call.
   For v1: accept UUIDs in display. Add a TODO for name resolution in v2.

4. **The `external_ref` URL-to-team routing is load-bearing for push UX.** If routing
   silently misidentifies which team owns an issue, pushes land in the wrong team with
   no feedback. The routing mechanism must be unambiguous (see Q1 in the PRD review).

---

## Open Questions

**UX-Q1: How prominently should primary-team semantics be surfaced at setup time?**

A user setting `linear.team_ids` for the first time needs to know that order matters.
Options:
- (a) Only in `--help` and docs (passive)
- (b) Print a note during `bd linear sync` if multi-team is detected for the first run:
  `Note: issues created without a team flag will sync to the first configured team (primary).`
- (c) `bd linear status` always labels `(primary)` explicitly

Recommendation: (b) + (c). First-sync note prevents the surprise; status labeling
reinforces the convention permanently.

**UX-Q2: Should `bd linear status` show per-team last-sync timestamps immediately, or
show UUIDs first and names in a follow-up API call?**

Displaying UUIDs-only is consistent with the rest of the config system but will look
ugly in practice. Fetching display names requires one API call per team at status time.
This is a product quality question, not a correctness one.

**UX-Q3: Does `bd linear teams` list configured teams only, or all teams the API key can see?**

The PRD leaves this open (Open Question 5). UX recommendation: default to configured teams
for `bd linear status`; add `--all` flag to `bd linear teams` to show discoverable teams.
This matches user intent: `status` is about "what am I syncing?" while `teams` is about
"what can I sync?"

---

## Integration Points

- **Data Model (data dimension):** The per-team `last_sync` key namespace
  (`linear.last_sync.<teamUUID>`) directly affects the `bd linear status` display.
  Status must read N keys, not 1, and format them per-team.

- **API & Interface (api dimension):** The `--strict` flag and exit code semantics must
  be specified in the CLI interface design. The `bd linear status` per-team display format
  should be coordinated with the api dimension to ensure consistent JSON output structure.

- **Integration (integration dimension):** The `validateLinearConfig()` function must be
  updated to accept either `linear.team_id` OR `linear.team_ids`. Current validation will
  reject multi-team configs with a confusing "missing team_id" error.

- **Scalability (scale dimension):** Fan-out to N teams multiplies API calls and error
  surfaces. UX for rate-limit errors ("team X rate-limited, retry in 60s") should be
  consistent across teams.

- **Security (security dimension):** Per-team API key support (if added in future) would
  change the config model significantly. Flag this as a v2 concern; v1 uses one key
  for all teams.
