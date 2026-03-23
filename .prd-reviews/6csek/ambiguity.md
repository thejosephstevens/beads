# Ambiguity Analysis

## Summary

The PRD for linear sync multi-team support is well-structured with clear non-goals and a
sensible fan-out approach. However, it contains several categories of ambiguity that will
cause engineers to make incompatible implementation choices independently. The most
significant issues are: a self-contradictory claim ("No storage changes needed" contradicts
Goal 4 requiring per-team timestamps), undefined routing semantics ("URL prefix match"
doesn't describe how team identity is extracted from Linear URLs), and multiple unresolved
open questions whose answers directly constrain the implementation of already-specified
scenarios.

Two of the five Open Questions (config key format and primary team for push) are not merely
nice-to-haves — they directly affect Scenarios 1, 2, 3, and the Rough Approach pseudocode.
An implementer reading the PRD today would be writing code against unresolved questions,
guaranteeing divergence from whatever the author intended.

## Findings

### Critical Gaps / Questions

**1. "URL prefix match" is undefined and likely inaccurate**

The Rough Approach states push routing uses "URL prefix match" on `external_ref` to identify
the target team. But Linear issue URLs are formatted as
`https://linear.app/<workspace>/issue/ENG-123` — the team identity is encoded in the issue
*identifier prefix* (`ENG-`), not as a URL path segment. Two issues from different teams
share the common URL prefix `https://linear.app/`. "URL prefix match" cannot mean a literal
string prefix comparison on the URL — that matches everything.

- Why this matters: two engineers will implement this differently. One will try to split on
  the team UUID (not present in the URL). Another will parse the issue identifier prefix
  (`ENG-`, `DESIGN-`). Neither is specified.
- The identifier prefix approach also requires a team-key-to-UUID mapping (since config
  stores UUIDs, not team keys), which is an undocumented prerequisite.
- Suggested clarifying question: Does push routing work by (a) matching the issue identifier
  prefix (e.g., `ENG-`) against a team's configured key, requiring a UUID→key mapping? Or
  (b) storing and matching full issue URLs against known URL patterns per team?

**2. "No storage changes needed" contradicts Goal 4**

The Constraints section states: "The Dolt config table is a flat key-value store." The Rough
Approach section then claims "No storage changes needed." But Goal 4 states "each team's
last-sync timestamp is tracked independently." The current implementation stores a single
`linear.last_sync` key. Independent per-team timestamps require multiple keys —
`linear.last_sync.<teamA>`, `linear.last_sync.<teamB>` — which IS a storage schema change
(new key namespace in the config table).

- Why this matters: if an engineer takes the "No storage changes needed" claim at face value,
  they will reuse the shared `linear.last_sync` key and silently break incremental sync for
  all but the last-synced team.
- Suggested clarifying question: Does "No storage changes needed" apply only to the Dolt
  issues table (not the config table)? Or is the claim incorrect and new config keys are
  required?

**3. "Primary team" is defined in Rough Approach, not in Goals or Scenarios**

Scenarios 2 and 3 reference the "primary team" concept as if it is already defined. The only
definition appears in the Rough Approach: "first in the `team_ids` list." This is buried in
an implementation suggestion, not a specification. The concept is never formalized as a
config property, has no name in the config schema, and is circular: "first in the `team_ids`
list" presupposes Open Question 1 (whether the key is `linear.team_ids` or indexed keys) is
already answered.

- Why this matters: Scenario 2 says "pushed to the primary team" as if this is a specified
  behavior. Implementers will treat the Rough Approach's positional definition as gospel,
  but it's just a suggestion in an approach section.
- Suggested clarifying question: Is "primary team" a formal concept? Should there be an
  explicit `linear.primary_team_id` config key, or is positional ordering the specified
  behavior?

**4. Open Question 1 (config key format) is unresolved but scenarios depend on it**

Open Question 1 asks whether multi-team config uses `linear.team_ids` (comma-separated) or
indexed keys (`linear.team_id.0`, `linear.team_id.1`). This is unresolved, but Scenario 1,
Scenario 4, and the Rough Approach Config Resolution Layer all assume `linear.team_ids`
(the comma-separated form). The indexed form would require completely different parsing and
a different config resolution ordering. The PRD cannot simultaneously leave this open and
write scenarios against one of the options.

- Why this matters: the answer constrains every config read/write path in the implementation.
  If indexed keys are chosen, the resolution layer logic in Rough Approach is wrong.
- Suggested clarifying question: Is `linear.team_ids` (comma-separated) the chosen format?
  If so, remove it from Open Questions and specify it. If not, update all scenarios.

**5. `--team` flag scope: push only, pull only, or both?**

Goal 5 says "override which team(s) to sync on a single invocation." Scenario 3 uses
`--push --team <uuid>` — a push-only override. Scenario 5 discusses status output without
mention of `--team`. The CLI additions section says "`bd linear sync --team <uuid>`
(repeatable) — override team list for this run." "Override team list for this run" implies
both pull and push are filtered. But Scenario 3's framing (as a push override to redirect a
new bead) implies it might be push-only.

- Why this matters: if `--team` filters pull too, a user running `bd linear sync --team
  <uuid>` only pulls from that one team, possibly leaving stale data for others. If it's
  push-only, pull always syncs all teams regardless of `--team`. These are very different
  behaviors.
- Suggested clarifying question: Does `--team` filter the pull operation, the push operation,
  or both? Is this a global "only operate on these teams" flag or a push-specific routing
  flag?

### Important Considerations

**6. "Should" vs "must" in Scenario 6**

Scenario 6 states: "The user sees a warning for Team A and is not blocked from Team B's
results." The word "sees" implies this must happen, but the construction is narrative, not
normative. The PRD uses "non-negotiable" explicitly for backward compatibility (Constraints)
but never applies equivalent language to error isolation. Open Question 4 leaves exit code
semantics unresolved. This creates a gray zone: partial failure must visually warn the user,
but whether the process must continue (vs. abort) and what the exit code must be is
unspecified.

- An implementer could reasonably treat "warn + continue" as the required behavior based on
  Scenario 6 alone, while another reads Open Question 4 as permission to abort on any error.

**7. Undefined scope of "backward compatibility"**

The Constraints section says "backward compatibility is non-negotiable" for single-`team_id`
users. But the scope is ambiguous: does this cover (a) config file format only, (b) output
format of `bd linear sync`, (c) exit codes, (d) all of the above? If `bd linear status` gains
per-team columns for multi-team users, does that break backward compat for single-team users
who have scripts parsing its output?

- Suggested clarifying question: Is backward compatibility defined at the config level only,
  or does it extend to command output format and exit codes?

**8. "Identical to today" in Scenario 4 — what counts as identical?**

Scenario 4 says a single-`team_id` user's behavior is "identical to today." But if the
implementation changes the internal code path (resolves `team_id` → `[]string{teamID}` →
fan-out loop with one element), the observable behavior might differ subtly: error messages
could change format, progress output could look different (e.g., "Syncing 1/1 teams...").
Is "identical" meant at the output/behavior level or the implementation level?

**9. "coexist without collision or confusion" — undefined terms**

Goal 3 says issues from different teams must "coexist without collision or confusion." Neither
"collision" nor "confusion" is defined. Collision could mean:
- Duplicate local IDs (same `id` in the beads table from two different teams)
- The same Linear issue appearing twice (e.g., cross-assigned issue)
- A push creating a duplicate issue in Linear

"Confusion" is even more vague — it has no measurable success criterion.

**10. Whether `bd linear sync` without flags does both pull and push, or only pull**

The PRD describes `bd linear sync --pull` and `bd linear sync --push` in the scenarios but
never states the default behavior when neither flag is given. Scenario 1 uses `--pull`,
Scenario 2 uses `--push`, Scenario 3 uses `--push --team`. The fan-out Rough Approach says
`tracker.Sync(opts)` but doesn't specify what opts defaults to. If the existing behavior
already defaults to pull-only or pull+push, this is a backward compat question; if not, the
default is unspecified.

### Observations

**11. Open Question 2 (primary team for push) and Scenario 2 are circular**

Scenario 2 describes a user pushing to the "primary team" as the expected behavior for new
beads without `external_ref`. Open Question 2 asks whether this should be "first-in-list
auto-selection" or "require `--team` flag." These are mutually exclusive behaviors: if
`--team` is required, Scenario 2's narrative breaks (the push would fail or prompt). The
PRD has already answered the question behaviorally in Scenario 2 but then re-opens it in
Open Questions.

**12. Indexed key format ambiguity with legacy key**

Open Question 1's indexed option (`linear.team_id.0`) creates a naming ambiguity with the
existing `linear.team_id` (no index) key. In a flat key-value config, both keys would coexist
simultaneously. The config resolution layer would need to distinguish "team_id with no index"
(legacy) from "team_id with index 0" (new). The PRD doesn't acknowledge this structural
conflict.

**13. No definition of "configured" for `bd linear teams`**

Open Question 5 asks whether `bd linear teams` lists "configured" teams or all API-accessible
teams. "Configured" itself is ambiguous: does it mean teams in `linear.team_ids` only, or
also `linear.team_id` (legacy), or also `LINEAR_TEAM_ID` env var? All three are resolved
into the team list per the Rough Approach but the source of truth for "configured" is unclear.

## Confidence Assessment

**Medium.** The PRD has clear goals, a well-bounded scope, and a sensible approach. The
non-goals are specific and help constrain interpretation. However, three of the five Open
Questions are entangled with already-specified scenarios, making the PRD internally
inconsistent: scenarios describe behaviors that depend on decisions the author explicitly
deferred. The "No storage changes needed" contradiction and the undefined "URL prefix match"
routing mechanism are the two highest-risk ambiguities — both will cause silent bugs if
implemented as literally written. An implementer needs answers to at least findings #1, #2,
#4, and #5 before writing code that handles push routing, config resolution, or error
isolation correctly.
