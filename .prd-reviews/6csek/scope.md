# Scope Analysis

## Summary

The PRD has a well-defined problem statement and a coherent non-goals section, but the boundary
between "this PRD" and "follow-up work" is blurry in three areas: the `bd linear teams` command,
`bd linear status` redesign, and per-team project filtering. These aren't currently called out as
in-scope or explicitly deferred, which means implementers will have to make scope decisions on
the fly. The MVP is identifiable but requires trimming two items that are framed as v1 requirements
but are actually v2 power-user features.

The largest scope risk is push routing. The PRD describes it as a simple URL-prefix match, but the
feasibility analysis confirms it requires a team key→UUID resolution step that isn't specified. This
will pull in more implementation work than the PRD implies, and without a design decision upfront,
the "simple fan-out" framing will collide with a non-trivial engineering problem mid-implementation.

## Findings

### Critical Gaps / Questions

**1. `bd linear teams` behavior is unscoped but will change**

- The PRD lists `bd linear teams` as Open Question #5 ("list configured teams or all teams
  the API key can access?") but does not mark it in-scope or out-of-scope. The command already
  exists. Multi-team support will implicitly change what it should show.
- If it's out of scope, the PRD should say so. If it's in scope, the work should be estimated.
  Leaving it as an open question means the implementer will make this call silently.
- **Clarifying question:** Is `bd linear teams` behavior change in scope for this PRD? If yes,
  which behavior — configured teams only, or all API-accessible teams?

**2. Scope of `bd linear status` redesign is underestimated**

- Scenario 5 ("Status shows all teams") is listed as a v1 requirement: "Output shows each
  configured team ID, its display name, and its last-sync timestamp."
- Displaying the team *display name* requires resolving UUID→name (an API call or cached mapping).
  The PRD doesn't scope this resolution step. The feasibility analysis notes the team key→UUID
  mapping isn't cached; name resolution is the same problem.
- If status only needs to show UUIDs and timestamps (no display name), this is trivial. If it
  needs display names, it requires the team name resolution infrastructure.
- **Clarifying question:** Does `bd linear status` need to show team display names (e.g.,
  "Platform") or is showing team UUIDs acceptable for v1?

**3. Push routing complexity is understated**

- The PRD says push routing is "URL prefix match" to route existing issues. The feasibility
  analysis found this requires a team key→UUID lookup (Linear URLs encode team *keys* like
  `ENG-`, not UUIDs). This lookup isn't specified in the PRD.
- The "Rough Approach" section treats push routing as a solved problem, but without the
  key→UUID resolution, there's no way to match a URL to a configured UUID.
- This is a scoping issue: the PRD implies push routing is simple, but the actual implementation
  requires either an API call at startup or storing `linear.team_key.<uuid>` in config.
- **Clarifying question:** How should the team key (from the Linear URL) be resolved to a
  configured team UUID? API fetch at startup? Require user to configure the mapping manually?

### Important Considerations

**4. `--team` CLI override (Goal #5 / Scenario 3) may be a v2 feature in disguise**

- Goals 1-4 define the core MVP: sync multiple teams, backward compat, issue coexistence,
  independent timestamps. Goal 5 (CLI override) and Scenario 3 (push to non-primary team)
  are power-user workflows that don't affect the core value proposition.
- A user who configures `team_ids` gets multi-team sync (Goals 1-4). The `--team` override
  only matters when they need to push to a specific non-primary team. This is an uncommon
  operation.
- Including `--team` in v1 adds CLI surface area and flag validation complexity for a use case
  most users won't hit until after they've used the feature for a while.
- **Suggested trim:** Flag `--team` as v2 and ship v1 without it. Scenario 3 users can set
  `linear.team_id` to the target team as a workaround.

**5. Team removal from config creates orphaned issues — not addressed**

- When a user removes a team from `linear.team_ids`, all previously synced issues from that
  team remain in the local database with their `external_ref` intact. There's no cleanup, no
  tombstoning, no warning.
- The PRD doesn't mention this case. For most users it's benign (the issues stay, they just
  don't update). But users who remove a team to "stop syncing" it will find the old issues
  persist indefinitely.
- This doesn't need to be solved in v1, but should be called out as a known limitation in the
  non-goals or open questions section.

**6. Exit code semantics for partial failure are unresolved but affect automation**

- Open Question #4 asks whether partial failure (one team errors) should be exit code 0 or
  non-zero. This isn't just a UX decision — it affects any CI/CD or scripting use of `bd linear sync`.
- Scenario 6 says "warn + continue." If exit code is 0 on partial failure, automation won't
  detect the failure. If non-zero, automation stops on a partial failure that the PRD says should
  be tolerated.
- The PRD should make a decision here before implementation, not leave it open. Convention for
  warn-and-continue tools (e.g., `make -k`) is typically non-zero exit on any failure. But that's
  a design call the author needs to make.

### Observations

**7. Single-team user path (Scenario 4) is correctly scoped as no-op**

- The backward compatibility guarantee ("zero change in behavior") is appropriate and achievable.
  The config resolution layer (`team_ids > team_id > env`) makes this clean.

**8. Sequential fan-out is the right MVP scope boundary**

- Deferring parallel execution to v2 is correct. The complexity of concurrent writes to the Dolt
  store and multi-team error aggregation don't belong in v1. This non-goal is well-placed.

**9. "No Dolt schema changes" non-goal contradicts the implementation**

- The PRD states "No storage changes needed" but the feasibility analysis found that per-team
  `last_sync` timestamps require new config keys (`linear.last_sync.<teamUUID>`). Writing new
  config keys IS a storage change (adding new rows to the config table). The non-goal should
  be reworded to "no schema changes" (no new columns/tables), not "no storage changes."

**10. Natural v1/v2 seam is clean**

- **v1 core:** Config resolution, sequential fan-out, per-team last_sync keys, backward compat,
  error isolation (warn + continue).
- **v1 optional:** `bd linear status` redesign (UUID display is trivial; display name requires
  resolution step).
- **v2:** `--team` CLI override, parallel sync, per-team project IDs, team name display.

## Confidence Assessment

**Medium.** The problem, non-goals, and user scenarios are clearly articulated. The core
fan-out pattern is well-scoped. The gaps are in the boundary cases: `bd linear teams`,
status display names, and push routing complexity. None of these are blockers to starting
implementation, but all three will cause scope disagreements mid-build if not resolved.
The `--team` override (Goal #5) is the clearest scope creep risk — it's framed as v1 but
is functionally v2. Addressing the three critical questions above would raise this to High.
