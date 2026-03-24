# Integration Analysis

## Summary

The Linear multi-team sync feature touches four integration boundaries: the CLI command layer
(`cmd/bd/linear.go`), the tracker adapter (`internal/linear/tracker.go`), the sync engine
(`internal/tracker/engine.go`), and the config store. The current architecture is deeply
single-team: the engine hardcodes `ConfigPrefix() + ".last_sync"` as a single key, `Init()`
fatally rejects configs without `linear.team_id`, and `runLinearSync()` creates exactly one
`Tracker` and one `Engine`. None of these assumptions hold in the multi-team world.

The migration path is additive, not destructive. No new tables, no interface changes to
`IssueTracker`, no changes to the `Engine` core logic. The key insight is that the multi-team
fan-out loop lives entirely in the CLI command layer — `runLinearSync()` instantiates one
`Tracker`/`Engine` pair per team and calls `Sync()` sequentially, accumulating results. The
engine, tracker interface, and storage layer remain unchanged. This keeps the blast radius
small and enables incremental delivery.

---

## Analysis

### Key Considerations

- **The engine's `last_sync` key is team-unaware.** `engine.go` constructs the key as
  `e.Tracker.ConfigPrefix() + ".last_sync"` — a single string, `"linear.last_sync"`. In
  multi-team mode, each `Tracker` must report a distinct `ConfigPrefix()` per team so the
  engine writes and reads the correct per-team cursor. This is the highest-impact integration
  point: get it wrong and incremental sync silently overwrites cross-team cursors.

- **`validateLinearConfig()` is a hard gate blocking multi-team.** It unconditionally
  requires `linear.team_id` and fails with a fatal error if absent. Multi-team configs
  set `linear.team_ids` instead. This function must accept either key.

- **`Tracker.Init()` duplicates `validateLinearConfig()` logic.** Both functions check
  for `linear.team_id` and `LINEAR_TEAM_ID`. In multi-team mode, `Init()` is called once
  per team with a pre-resolved UUID — the caller passes the team ID, not the config key.
  This changes the instantiation contract.

- **Push routing requires `external_ref` → team resolution.** When pushing, the engine
  iterates all local issues. In multi-team mode, each team's `Engine` must only push issues
  that belong to its team. The existing `ShouldPush` hook in `PushHooks` is the right
  extension point — but the routing logic (URL parsing + team-key cache) must be wired in
  per-team.

- **`runLinearStatus()` is single-team today.** It reads `linear.team_id` and
  `linear.last_sync` as single values. Multi-team requires iterating `linear.team_ids`,
  reading per-team keys, and reformatting output. This is a pure display change with no
  engine coupling.

- **`getLinearClient()` returns a single-team client.** It's called from `runLinearTeams()`.
  That command does not need multi-team awareness for v1 (listing all accessible teams is
  team-agnostic via the API key).

- **Error aggregation is a new concern.** Today, `runLinearSync()` calls `engine.Sync()`
  once and either succeeds or fails. Multi-team fan-out means partial failure is now possible.
  The CLI must accumulate per-team results and decide on final exit code based on `--strict`.

---

### Options Explored

#### Option 1: Fan-out in CLI command layer (no engine changes)

- **Description**: `runLinearSync()` reads `linear.team_ids`, splits into a list of UUIDs,
  and iterates: for each UUID, construct a `linear.Tracker` with the team UUID pre-loaded,
  call `lt.Init()` with a team-scoped config view, create an `Engine`, call `Sync()`.
  Accumulate results. The engine and tracker interface are unchanged.
- **Pros**: Minimal blast radius. No engine API changes. Each team sync is fully isolated —
  a failure in team B does not corrupt team A's `last_sync`. Easy to test independently.
  Aligns with "fan-out at the outermost layer" principle.
- **Cons**: Requires `Tracker.Init()` to accept a team UUID directly (not just read from
  config), OR requires a per-team config view abstraction. Slightly more code in the CLI.
- **Effort**: Medium

#### Option 2: Multi-team fan-out inside the Engine

- **Description**: The engine receives a `[]IssueTracker` (one per team) and fans out
  internally, running pull/push for each, aggregating results.
- **Pros**: Single `engine.Sync()` call from CLI. Centralized error handling.
- **Cons**: Changes the `Engine` API — breaking change for all callers (tests, other
  trackers, etc.). The engine is currently team-unaware by design; coupling multi-team
  semantics into it makes the engine harder to reuse for single-team trackers.
  Over-engineering for a 2-5 team fan-out.
- **Effort**: High

#### Option 3: Wrapper tracker that delegates to per-team trackers

- **Description**: A `MultiTeamTracker` implements `IssueTracker` and internally holds
  `[]*linear.Tracker`. Fetch, Create, Update are dispatched by team.
- **Pros**: Single engine call. Transparent to CLI.
- **Cons**: The `IssueTracker` interface is not designed for multi-team dispatch — methods
  like `CreateIssue` operate on one issue at a time, with no routing context. Push routing
  (which team does this issue belong to?) must be embedded in the wrapper, adding invisible
  coupling. The abstraction leaks.
- **Effort**: High

#### Option 4: Per-team config scoping via config prefix override

- **Description**: `Tracker.ConfigPrefix()` returns `"linear.<uuid>"` instead of `"linear"`,
  making all engine config key construction automatically per-team
  (`linear.<uuid>.last_sync`, etc.).
- **Pros**: Zero engine changes. The engine's `ConfigPrefix() + ".last_sync"` pattern
  becomes correct for multi-team automatically.
- **Cons**: `linear.<uuid>.api_key` etc. don't exist — the API key is global. The prefix
  override applies to ALL config reads in the tracker, not just `last_sync`. Would require
  the tracker to route different keys to different namespaces (per-team vs. global). Complex.
- **Effort**: Medium (but fragile)

### Recommendation

**Option 1 (fan-out in CLI) combined with a targeted `ConfigPrefix()` override for
`last_sync` only.**

Concretely:

1. **`Tracker` gets a `teamID` constructor parameter.** Add `NewTracker(teamID string)` or
   extend `Init()` to accept an override. When `teamID` is provided, `ConfigPrefix()`
   returns `"linear." + teamID`. When absent (backward compat), returns `"linear"`.

2. **`validateLinearConfig()` accepts `linear.team_ids` OR `linear.team_id`.** Returns a
   `[]string` of UUIDs to iterate. Existing single-team configs are wrapped in a
   single-element slice — the caller loop is identical in both cases.

3. **`runLinearSync()` loops over UUIDs.** For each UUID: create a `Tracker` with that UUID,
   call `Init()` (which reads global `linear.api_key`), create an `Engine`, call `Sync()`.
   Accumulate `SyncResult`s. Apply `--strict` logic for exit code.

4. **Per-team `ShouldPush` hook.** Wired per-team in `buildLinearPushHooks()`: parse the
   `external_ref` URL identifier prefix (e.g., `ENG-123` → `ENG`), look up
   `linear.<uuid>.team_key` in config. If match → push. If no team_key cached → push to
   primary team only (safe fallback).

5. **`runLinearStatus()` iterates `team_ids`.** For each UUID, read
   `linear.<uuid>.last_sync`. Format as per-team table. Label first entry as `(primary)`.

This approach touches 3 files (`linear.go`, `tracker.go`, engine remains untouched) and
adds ~100-150 lines net.

---

## Constraints Identified

1. **`Engine.Sync()` writes `last_sync` using `ConfigPrefix() + ".last_sync"` unconditionally.**
   If `ConfigPrefix()` returns `"linear"` (the current behavior), multi-team sync overwrites
   a shared key. The `ConfigPrefix()` override (returning `"linear.<uuid>"`) is REQUIRED for
   correct incremental sync. This is a hard constraint.

2. **`Tracker.Init()` currently reads `linear.team_id` from config.** In multi-team mode,
   the CLI must pass the UUID directly (after reading `linear.team_ids`). `Init()` cannot
   read `linear.team_ids` itself — it would need to know which team to initialize for.
   The instantiation contract must change: either a constructor argument or an `InitWithTeam()`
   variant.

3. **`PushHooks.ShouldPush` is the only push filter extensible per-team.** The engine's
   `doPush()` iterates ALL local issues and calls `ShouldPush` for each. Multi-team routing
   is implemented here — each team's hooks closure captures its UUID and filters by
   `external_ref` prefix. This is sufficient but means each team's engine scans the full
   issue set. Acceptable for v1 (O(N) with small N).

4. **`linear.project_id` (global) is deprecated in multi-team mode.** The engine reads
   `ConfigPrefix() + ".project_id"` — but wait, the project_id is set on the `Client`
   directly in `Init()` via `client.WithProjectID()`. With `ConfigPrefix()` returning
   `"linear.<uuid>"`, the engine would look for `linear.<uuid>.project_id`, which is the
   per-team key. This is actually the CORRECT behavior if we adopt Option 1 — the per-team
   key takes over naturally. The global `linear.project_id` must be explicitly checked only
   during migration (warn if set alongside `team_ids`).

5. **`DetectConflicts()` uses `ConfigPrefix() + ".last_sync"`.** This function is called
   between pull and push in bidirectional sync (see `engine.go:137`). With per-team
   `ConfigPrefix()`, it reads the correct per-team cursor. No change needed if constraint 1
   is satisfied.

6. **Single-team backward compatibility is a hard requirement.** Existing users with
   `linear.team_id` (no `linear.team_ids`) must see identical behavior. The fan-out loop
   wraps their config in a single-element slice; `Tracker.ConfigPrefix()` returns `"linear"`
   (no UUID suffix) when initialized in single-team mode. The engine writes
   `linear.last_sync` as before.

---

## Open Questions

**INT-Q1: How does `Tracker.Init()` receive the team UUID in multi-team mode?**

Two clean options:
- (a) Add a `teamID string` field to `Tracker` before calling `Init()`:
  `lt := &linear.Tracker{TeamID: uuid}; lt.Init(ctx, store)`
- (b) Add `InitWithTeam(ctx, store, teamID string)` — explicit but adds API surface

Recommendation: (a). Exported `TeamID` field on `Tracker` is idiomatic Go. `Init()` checks
`t.TeamID` first; falls back to reading `linear.team_id` from config. Zero backward compat
breakage.

**INT-Q2: Should the per-team fan-out loop be sequential or concurrent?**

Sequential is safer for v1: simpler error handling, no store concurrency concerns (Dolt
serializes writes anyway), and 2-5 teams at typical Linear API latencies adds only 1-4 extra
seconds per team. Concurrency buys little and adds surface for partial-write bugs in
`last_sync`. **Recommendation: sequential.**

**INT-Q3: How should merge-queue (beads → Linear push) handle new issues with no `external_ref`?**

When pushing a new issue with no `external_ref`, the engine calls `Tracker.CreateIssue()`.
In multi-team mode, which team receives the new issue? The current `ShouldPush` hook filters
by prefix — but new issues have no prefix to match. The primary team (first UUID in
`linear.team_ids`) should receive all new issues with no prefix constraint. The `ShouldPush`
hook must handle this case explicitly: if `external_ref` is nil AND this is the primary team,
return true; if `external_ref` is nil AND this is NOT the primary team, return false.

**INT-Q4: Where does per-team `team_key` caching happen?**

The data model analysis recommends lazy population (first sync per team triggers a `GET
/teams` API call to fetch the key). The integration question is: which function makes this
call? Candidate: inside `Tracker.Init()` (if `linear.<uuid>.team_key` is absent, fetch from
API and write to config). One extra API call per team, amortized after first sync. Acceptable.

---

## Integration Points

**Data Model dimension:**
The `linear.<uuid>.last_sync`, `linear.<uuid>.team_key`, and `linear.<uuid>.project_id` keys
defined in the data model are read/written by the engine via `ConfigPrefix() + ".last_sync"`.
The `ConfigPrefix()` override (`"linear.<uuid>"`) is the integration contract between this
dimension and the data model. If the data model changes the key format, this contract breaks.

**UX dimension:**
The `ShouldPush` logic for new issues (INT-Q3) directly affects UX: a user who runs
`bd linear sync --push` expects new issues to go to the primary team. If the fan-out loop
sends the new issue to all teams (because no `external_ref` filter fires), the issue is
created in every configured team — a silent data duplication bug. The UX doc's
recommendation (primary team receives new issues) must be enforced at the integration layer.

**API & Interface dimension:**
- `validateLinearConfig()` return type changes from `error` to `([]string, error)` (list of
  UUIDs to iterate). This is a pure internal function — not exposed via the tracker interface.
  No API contract change.
- `runLinearStatus()` output format expands to N-team table. The JSON output shape changes:
  `team_id` becomes `teams: [{id, key, last_sync, ...}]`. Callers of `bd linear status --json`
  are affected.

**Security dimension:**
No new secrets. `linear.api_key` is read once and shared across all team `Tracker`
instances. Each team's `Client` is initialized with the same key but a different `teamID`.

**Scalability dimension:**
Sequential fan-out means sync latency is `O(N * per-team-latency)`. For N=5 teams and
~2s per team, that's 10s total — acceptable. The engine's `doPull()` and `doPush()` each
scan all local issues once per team invocation. For 10k issues and 5 teams, that's 50k
in-memory iterations with Dolt reads — acceptable for v1. If N grows beyond 10, revisit.

## Implementation Plan

### Phase 1: Config layer (unblock everything else)

1. Update `validateLinearConfig()` to return `([]string, error)` — a list of UUIDs parsed
   from `linear.team_ids` OR a single-element slice from `linear.team_id`.
2. Add exported `TeamID` field to `linear.Tracker`.
3. Update `Tracker.Init()` to use `t.TeamID` if set; fall back to `linear.team_id` from
   config if unset (backward compat path).
4. Update `Tracker.ConfigPrefix()` to return `"linear." + t.TeamID` when `t.TeamID != ""`,
   otherwise `"linear"`.

### Phase 2: CLI fan-out

5. Rewrite `runLinearSync()` to loop over the UUID list from `validateLinearConfig()`.
   Instantiate `Tracker{TeamID: uuid}` and `Engine` per iteration. Accumulate results.
6. Implement `--strict` exit code logic in the accumulator.
7. Update `buildLinearPushHooks()` to accept a `teamUUID` and `isPrimary bool` parameter.
   The `ShouldPush` closure routes by `external_ref` prefix + primary-team fallback.

### Phase 3: Status and display

8. Rewrite `runLinearStatus()` to iterate team UUIDs and display per-team table.
9. Update JSON output shape for `bd linear status --json`.

### Phase 4: team_key caching (enables push routing)

10. In `Tracker.Init()`, if `TeamID != ""` and `linear.<uuid>.team_key` is absent, call
    `client.GetTeam(ctx, uuid)` once and write the key to config.
