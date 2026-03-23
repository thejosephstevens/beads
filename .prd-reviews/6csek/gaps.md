# Missing Requirements

## Summary

The PRD for linear sync multi-team support has a solid core — backward compatibility and
fan-out orchestration are well-considered. However, several operational and UX details are
either left as open questions or absent entirely. The most dangerous gaps are around config
command UX (how does a user actually write a multi-value config key?), per-team project_id
resolution (an unresolved open question that directly affects query correctness), and partial
failure exit code semantics (critical for any scripting/CI usage). The "primary team" concept
is introduced narratively but never formally defined in the config model.

Without answers to the critical questions below, an implementer will make arbitrary decisions
that either break scripting users or diverge from user expectations — generating follow-up
bugs rather than shipping the feature cleanly.

## Findings

### Critical Gaps / Questions

**1. Config command UX for multi-value key**
The PRD defines `linear.team_ids` as a comma-separated value but says nothing about how a
user actually sets it. The current `bd config set <key> <value>` interface is a flat
string setter. Does `bd config set linear.team_ids "UUID1,UUID2"` work? What if the user
uses spaces instead of commas? What if a UUID contains a comma (unlikely but unspecified)?
There is no `bd config append` or similar command mentioned anywhere.
- Why this matters: users will immediately hit this on first use; without guidance they'll
  guess wrong and corrupt their config or hit parsing errors.
- Question: Does the PRD intend a new `bd config append` subcommand, or is the UX to
  manually construct a comma-separated string with `bd config set`?

**2. Per-team `project_id` (Open Question 3 — left unresolved but blocking)**
Today's `linear.project_id` filters which Linear issues are synced. With multiple teams,
each may have a different project filter (or no filter). The PRD leaves this as an open
question without a proposed answer. If left as-is, a user with `linear.project_id` set will
either (a) silently apply the same project filter to all teams — wrong if teams have
different projects — or (b) have the filter ignored for Team B — also wrong.
- Why this matters: project filtering affects correctness of every pull operation.
- Question: For v1, is per-team project_id out of scope (and the existing project_id
  applies to all teams), or do we need `linear.project_id.<team_uuid>`? This must be
  decided before implementation.

**3. Partial failure exit codes (Open Question 4 — left unresolved)**
The PRD proposes "warn + continue" for one-team failures (Scenario 6) but explicitly leaves
the exit code question open. This is not just a UX preference — it determines whether
`bd linear sync && do_something` is safe in scripts, Makefiles, and CI pipelines.
- Why this matters: if partial success returns exit 0, scripting users will silently miss
  sync failures and operate on stale data.
- Question: Is exit code non-zero on any partial failure, or is it zero with warning output?
  Suggest: exit 0 with `--strict` flag for non-zero on any error.

**4. "Primary team" definition in config model**
Scenario 2 and the Rough Approach both refer to the "primary team" as the push destination
for new issues without `external_ref`. The concept is defined as "first in the `team_ids`
list" but this is only stated in the Rough Approach section, not as a formal config
property. There is no `linear.primary_team_id` key, no `bd config` output showing which
team is primary, and no user-visible way to change primary team without reordering the
list.
- Why this matters: users will create issues expecting them to land in the right team, and
  implicit ordering is fragile.
- Question: Should primary team be explicit (`linear.primary_team_id`) or is positional
  acceptable? What does `bd linear status` show to communicate which team is primary?

**5. `--team` flag with unrecognized UUID**
Scenario 3 introduces `--team <uuid>` to override push destination. The PRD says nothing
about what happens if the UUID is not in the user's configured `team_ids`. Should the CLI
reject it (enforce configured-teams-only), warn and proceed (useful for one-off pushes),
or silently accept it?
- Why this matters: silent acceptance could push to unexpected teams; rejection blocks
  legitimate one-off use.
- Question: Should `--team` be limited to configured teams, or can it address any team the
  API key can reach?

### Important Considerations

**6. Config migration UX — from `team_id` to `team_ids`**
The PRD correctly specifies backward compatibility, but there is no guidance on the upgrade
path. A user who wants to add a second team must know to use `linear.team_ids` (plural) and
that their existing `linear.team_id` is now shadowed. Without a migration warning (e.g.,
"you have `linear.team_id` set; to add teams, use `linear.team_ids`"), users will
unknowingly run with the old config.
- Suggestion: When `linear.team_ids` is set and `linear.team_id` also exists, print a
  deprecation notice suggesting the user remove the singular key to avoid confusion.

**7. Linear API rate limits under fan-out**
Multi-team sync multiplies API call volume by the number of teams. Linear's API rate limit
is 1500 req/10min (OAuth) or 60 req/min (personal keys). A user with 5 teams could hit
limits significantly faster.
- The PRD says no rate limiting logic is needed, but there is no acknowledgment of this
  risk.
- Suggestion: Document the rate limit implication and note that sequential fan-out (already
  proposed) provides natural throttling between teams.

**8. Per-team last-sync timestamp storage format**
Goal 4 states timestamps are tracked independently per team, but there is no spec for the
config key name. Is it `linear.last_sync.<team_uuid>`, `linear.last_sync_<slug>`, or a new
metadata table? The Rough Approach section omits this entirely.
- Why this matters: the key format is part of the schema and affects future tooling.
- Suggestion: Specify the key format explicitly (e.g., `linear.last_sync.<team_uuid>`) in
  the Constraints or Rough Approach section.

**9. `bd linear teams` command scope (Open Question 5 — unresolved)**
The PRD introduces `bd linear teams` but leaves Open Question 5 (configured vs. all-API-accessible teams) unresolved. These have very different UX implications — listing only configured teams is a config inspector; listing all-accessible teams is a discovery tool.
- Suggestion: For v1, list only configured teams (simpler, no extra API call). Discovery of
  all teams can be a separate `bd linear teams --discover` subcommand.

**10. Error output format for partial failures**
Scenario 6 says "user sees a warning for Team A." But there is no spec for what that warning
looks like (stderr vs. stdout, structured vs. prose), whether it includes the team UUID or
name, or whether it persists anywhere (e.g., bead notes, last-error config key).
- Why this matters: silent or poorly-formatted warnings will be missed in terminal noise.
- Suggestion: Print to stderr in a consistent format: `[warn] team <uuid>: <error>`, and
  write the last error to `linear.last_error.<team_uuid>` in config so `bd linear status`
  can surface it.

### Observations

**11. `--team` flag applies to pull as well as push — not fully specified**
Scenario 5 in the CLI additions mentions `--team <uuid>` (repeatable) for overriding the
team list "for this run." Goal 5 says "override which team(s) to sync on a single
invocation." But Scenario 3 uses `--push --team` specifically. It is unclear whether
`--team` also filters the pull side of a `bd linear sync` (pull + push). Implementers will
need to decide.

**12. Duplicate UUID in `team_ids`**
If a user accidentally configures the same UUID twice (e.g., `linear.team_ids = UUID1,UUID1`),
the fan-out will sync and push to the same team twice. The PRD does not address deduplication.
This is low priority but could cause confusing duplicate issues on push.

**13. Display name resolution for `bd linear status`**
Scenario 5 says "output shows each configured team ID, its display name, and its last-sync
timestamp." Fetching display names requires an extra API call per team (`GET /teams/<id>`).
This is a latency concern for `bd linear status` with many teams. The PRD does not mention
caching team display names.

**14. Interaction with `external_ref` URL-prefix matching for routing**
The Rough Approach uses URL prefix matching on `external_ref` to route issues to the correct
team on push. The Linear URL format (`https://linear.app/issue/ENG-123`) encodes the issue
identifier prefix, not the team UUID. If two teams have issue identifiers that share a
prefix (e.g., `ENG` and `ENGG`), prefix matching could mismatch. The PRD does not address
this edge case.

## Confidence Assessment

**Medium.** The PRD is well-scoped and the non-goals are clear, which reduces surface area.
The fan-out orchestration approach is sound. However, several operational details critical
for correctness (per-team project_id, exit codes) and user experience (config UX, primary
team definition) are either explicitly deferred as open questions or absent entirely. The
implementation will inevitably encounter these during coding and make ad-hoc decisions
without a spec to reference — increasing the risk of inconsistency across the feature.
