# Stakeholder Analysis

## Summary

The PRD identifies only one user type — engineers on 2+ Linear teams — but the feature
touches a broader population. Existing single-team users, CI/automation users, workspace
administrators, security teams, and future maintainers of the `bd` codebase all have
stakes in how this lands. Most of these groups are not mentioned anywhere in the document.

The most significant conflict is between **scripting/CI users** (who need deterministic
exit codes and machine-parseable output) and **interactive users** (who want warn-and-continue
semantics). The PRD explicitly defers this decision as Open Question 4, but it is not merely
a preference — it is a correctness question for any user running `bd linear sync` in
automation. A second conflict exists between **security-conscious workspace administrators**
(who may want per-team API key scoping) and **power users** (who want a single credential
to sync all teams). The multi-team feature structurally favors the latter and has no
story for the former.

## Findings

### Critical Gaps / Questions

**1. Scripting/CI users — conflicting exit code needs (Open Question 4 unresolved)**

- The PRD defines Scenario 6 as "warn + continue" but leaves exit code semantics open.
  CI users running `bd linear sync && deploy_from_issues` will silently operate on stale
  data if partial failure returns exit 0. Interactive users want warn-and-continue. These
  are irreconcilable without an explicit mechanism (e.g., a `--strict` flag).
- Why this matters: silent partial failures in CI are a data-correctness issue, not just
  a UX preference. A user who deploys from issue state after a partial sync may act on
  stale data.
- Suggested clarifying question: Should partial failure be exit 0 (with warning output to
  stderr) by default, with a `--strict` flag for non-zero on any error? Or the inverse:
  fail by default, with `--partial-ok` for warn-and-continue?

**2. Existing users with `linear.project_id` — silent data loss risk**

- Users who have configured `linear.project_id` today will have that filter applied to
  ALL teams when they add a second team. If Team A uses project X and Team B uses project Y,
  Team B's issues will be silently excluded when project X's filter is applied.
- This is a current-user correctness regression, not just a new-user gap. These users are
  not identified in the PRD's affected-users section.
- Why this matters: a user adding a second team for the first time could lose visibility
  into all of that team's issues, with no error message.
- Suggested clarifying question: Should the CLI warn when `linear.project_id` is set and
  multiple teams are configured? Is per-team project_id in or out of scope for v1?

**3. Workspace administrators — no story for per-team access control**

- The PRD implicitly assumes a single API key grants access to all configured teams. Workspace
  admins in security-sensitive orgs may need to restrict which local machines can pull from
  which teams (e.g., contractors on PAYMENTS should not pull SECURITY team issues).
- There is no `linear.team_api_key.<uuid>` or equivalent mechanism. The PRD's Non-Goals do
  not address this — it's simply absent.
- Why this matters: an admin who wants to restrict team access cannot do so. The feature
  makes it easier for users to expand their sync scope without admin visibility.
- Suggested clarifying question: Is per-team API key configuration in scope (even as a
  future consideration)? Should the docs warn that multi-team requires a key with access
  to all configured teams?

### Important Considerations

**4. Existing single-team users — output format changes may break scripts**

- `bd linear status` and `bd linear sync` output format will change when multi-team display
  is added. Even users with a single `linear.team_id` may see output restructured
  (e.g., per-team sections, team UUID prefix on each line). Scripts parsing current output
  will break silently.
- The backward-compatibility constraint focuses on config behavior, not output format.
- Suggestion: Specify that output format changes are gated behind multi-team config. If
  only `linear.team_id` is set, output is identical to today.

**5. Security teams — API key scope expansion is unacknowledged**

- One API key that syncs N teams has a broader blast radius if leaked or misused. Security
  teams may want to know that their API key configuration is now implicitly scoped to
  multiple teams.
- There is no mention of what permissions the API key needs (read/write on each team? org-level?).
- Suggestion: Document the minimum required Linear API key permissions for multi-team sync
  (read access to each configured team). This helps security reviewers scope the credential.

**6. `bd` codebase maintainers — developer experience of the fan-out layer**

- The fan-out logic in `cmd/bd/linear.go` adds a new orchestration layer that all future
  contributors must understand. The PRD does not mention documentation, code comments, or
  design notes that would help future maintainers.
- The constraint "don't modify Tracker/Client internals" is correct but increases the
  cognitive complexity of `linear.go` itself.
- Suggestion: Require an inline comment block in `linear.go` explaining the fan-out pattern
  and the config resolution priority order, so the architecture is self-documenting.

**7. Users who installed `bd` from package managers — config migration UX**

- Users upgrading from a version with `linear.team_id` to one with `linear.team_ids` get
  no migration assistance. The PRD says backward compat is maintained, but there is no
  mention of upgrade path documentation, `bd linear config check`, or a migration note
  in CHANGELOG.
- Why this matters: user confusion at upgrade time generates support load.
- Suggestion: Add CHANGELOG entry and `bd linear status` output that flags when the old
  `linear.team_id` key is in use and suggests migration path to `linear.team_ids`.

**8. Third-party tooling authors — `external_ref` URL format contract**

- Any tooling that consumes `external_ref` values (scripts, dashboards, other syncing tools)
  relies on the URL format `https://linear.app/issue/<TEAM-KEY>-<num>`. The multi-team
  feature does not change this format, but the team routing logic now depends on it.
- There is no mention of this URL format as a public contract or a concern for downstream
  tooling.
- Suggestion: Document the `external_ref` URL format in the config schema or developer
  docs so third-party integrators know it is stable.

### Observations

**9. "Reported by a real user" is not enough stakeholder coverage**

- The problem statement cites a single reporter (steveyegge/beads#2791). A stakeholder
  analysis for a feature like this should include user research with 2-3 other engineers
  in multi-team orgs to confirm that the proposed config model (`team_ids`, primary team
  concept, `--team` flag) matches actual user mental models.
- This is not a blocker, but shipping based on one reporter's request risks design
  decisions that feel right to one user and confuse others.

**10. Launch coordination — docs team and changelog author not identified**

- The PRD has no section on who needs to be notified at launch: docs owners, changelog
  maintainers, users already on the beta list (if any). This is a process gap rather than
  a technical one, but launch without updated docs creates immediate support load.

**11. Users with parent/child Linear team structure**

- The PRD mentions "parent/child team structure" in the problem statement but never revisits
  it. Linear's parent/child teams have hierarchical issue inheritance — issues in a child
  team may also appear in the parent. A user syncing both parent and child teams could see
  the same issue pulled twice (via two tracker.Sync() calls).
- The de-duplication logic is not addressed in the PRD.

**12. New multi-team users vs. non-goal: automatic team discovery**

- The Non-Goals explicitly exclude automatic team discovery. But users in large orgs (10+
  teams) who want to sync all their teams must manually collect all UUIDs. The `bd linear teams`
  command exists (per Open Question 5) but its scope is unresolved. There is a latent
  conflict between the "no wizard" constraint and the UX burden on users in large orgs.

## Confidence Assessment

**Medium.** The PRD is clear about who it's trying to help (multi-team engineers) and what
it won't do (cross-workspace, auto-discovery). But it treats "existing single-team users"
as a constraint to satisfy rather than a stakeholder to reason about, and it does not
mention operators, security teams, CI users, admins, or future maintainers at all. The
most significant conflict — CI/scripting users vs. interactive users on exit code semantics
— is explicitly deferred as an open question. Until that question is answered, the feature
cannot be considered safe for automation use cases, which are likely a significant portion
of actual `bd linear sync` invocations.
