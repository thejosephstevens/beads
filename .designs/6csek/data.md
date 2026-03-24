# Data Model Design

## Summary

The Linear multi-team sync feature requires extending the config key-value store to support
multiple team identities and independent per-team sync cursors. No new Dolt tables or columns
are needed: the existing flat KV config table (`config` / `store.SetConfig`) is sufficient
as long as we adopt a namespaced key convention. The bulk of the design work is choosing
key names, migration semantics, and lifecycle rules — not schema design.

The single structural concern is the `external_ref` URL already stored on each issue.
Push routing for existing issues depends on resolving which team owns a given URL, and the
resolution strategy chosen here has cascading implications for the API and integration
dimensions. This document proposes a team-key prefix cache as the tie-breaker.

## Analysis

### Key Considerations

- The config store is a flat KV table; namespaced keys (`linear.<teamUUID>.last_sync`) are
  the natural extension pattern. No migration or schema change required.
- `linear.last_sync` is a single shared key today. Multi-team requires a cursor per team;
  reusing the shared key for any team would silently break incremental sync for all others.
- `linear.team_id` (singular) is used in `validateLinearConfig()` and `Init()` with an
  explicit error on absence. Both call sites must be updated to accept `linear.team_ids`
  as an alternative; neither may silently shadow the other.
- `external_ref` is already stored on each issue. The URL format is
  `https://linear.app/<workspace>/issue/<IDENTIFIER>` where `IDENTIFIER` encodes the team
  key (`ENG-123`, `INFRA-45`). The identifier prefix IS the routing signal — but it is not
  currently stored in config, so push routing cannot be resolved without an API call or a
  cached lookup.
- `linear.project_id` (global filter) applied to all teams in multi-team mode silently
  excludes issues from teams that don't belong to that project. This is a data correctness
  regression for existing users who add a second team.

### Options Explored

#### Option 1: Namespaced config keys (no schema change)

**Description:** Extend the flat KV store with namespaced keys. Team list stored as
`linear.team_ids` (comma-separated UUIDs). Per-team data stored under
`linear.<teamUUID>.<field>` (e.g., `linear.abc-123.last_sync`). Backward compat via
`linear.team_id` → `linear.team_ids[0]` coercion at read time.

```
linear.team_ids        = "uuid1,uuid2"
linear.uuid1.last_sync = "2026-03-24T12:00:00Z"
linear.uuid2.last_sync = "2026-03-24T08:30:00Z"
linear.uuid1.project_id = "proj-aaa"    # optional per-team filter
linear.uuid2.project_id = "proj-bbb"    # optional per-team filter
linear.uuid1.team_key   = "ENG"         # cached identifier prefix
linear.uuid2.team_key   = "INFRA"       # cached identifier prefix
```

- **Pros:** Zero schema migration. Follows existing `linear.priority_map.*` and
  `linear.state_map.*` prefix patterns already in the codebase. Backward compat is
  purely in application logic (read `team_ids`, fall back to `team_id`). Push routing
  resolution via cached `team_key` avoids per-push API round-trips.
- **Cons:** Keys are long. No referential integrity (orphaned per-team keys if a team is
  removed from `team_ids`). Listing "all configured teams" requires parsing `team_ids`
  then looking up each sub-key. `GetAllConfig()` returns the full table; callers must
  filter by prefix.
- **Effort:** Low

#### Option 2: Separate `linear_teams` table

**Description:** Add a new Dolt table `linear_teams(team_uuid TEXT PK, team_key TEXT,
project_id TEXT, last_sync DATETIME)`. Config remains for single-value settings.

- **Pros:** Clean relational model. Simple to query "all configured teams". Referential
  integrity via table-level operations. `last_sync` as a real datetime column enables
  range queries.
- **Cons:** Requires schema migration. Adds surface area to the `Storage` interface
  (new CRUD methods or a generic table accessor). Inconsistent with how all other tracker
  config is stored (all use flat KV). Overkill for a list that will typically be 2-5 teams.
- **Effort:** Medium-High

#### Option 3: JSON blob in a single config key

**Description:** Store all team config as a JSON value under `linear.teams`:
`[{"uuid":"...","key":"ENG","last_sync":"...","project_id":"..."}]`

- **Pros:** Single key to read/write. Self-contained.
- **Cons:** Cannot update `last_sync` for one team without parsing and re-serializing the
  entire blob. Races in concurrent sync (unlikely but possible). Not aligned with
  `bd config get/set` ergonomics (users can't `bd config set` a JSON array easily).
  Difficult to inspect with standard tools.
- **Effort:** Low (writing) but High (operationally)

### Recommendation

**Option 1: Namespaced config keys.**

The flat KV store with namespaced keys is the right choice for this feature scope:

1. Zero schema migration. No Storage interface additions.
2. Follows the established prefix pattern (`linear.priority_map.*`,
   `linear.state_map.*`, `linear.push_prefix`).
3. Backward compat is clean: at config read time, if `linear.team_ids` is absent, coerce
   `linear.team_id` to a single-element list. The old key is never deleted.
4. Cache the team identifier prefix (`linear.<uuid>.team_key = "ENG"`) at setup time
   (during `bd linear sync --setup` or at first sync). This eliminates per-run API calls
   for push routing.

Full proposed key set:

| Key | Type | Required | Notes |
|-----|------|----------|-------|
| `linear.team_ids` | `string` (CSV of UUIDs) | Yes (v1) | Replaces `linear.team_id` as primary config |
| `linear.team_id` | `string` (UUID) | Legacy | Read-only backward compat; coerced to `team_ids[0]` |
| `linear.<uuid>.last_sync` | `string` (RFC3339) | Managed | Written by engine after each per-team sync |
| `linear.<uuid>.project_id` | `string` | Optional | Per-team project filter |
| `linear.<uuid>.team_key` | `string` | Optional | Cached identifier prefix for push routing |
| `linear.last_sync` | `string` (RFC3339) | Legacy | Kept for single-team backward compat |
| `linear.project_id` | `string` | Deprecated | Global; warn when set + multi-team active |
| `linear.api_key` | `string` | Yes | Unchanged |
| `linear.api_endpoint` | `string` | Optional | Unchanged |
| `linear.push_prefix` | `string` | Optional | Unchanged |

## Constraints Identified

1. **No new Dolt tables or columns.** This is both a PRD constraint ("No storage changes
   needed" meant schema, not KV rows) and a correctness boundary: every Storage
   implementation (DoltStore, EmbeddedDoltStore, mocks) would need updating for a new
   table.

2. **`linear.team_id` must remain readable.** Existing deployments have this key. The
   migration path is: on first multi-team config write, write `linear.team_ids` without
   deleting `linear.team_id`. Code reads `team_ids` first; falls back to `team_id`.

3. **`linear.last_sync` (shared key) must not be retired in v1.** Single-team users
   (those with only `linear.team_id` and no `linear.team_ids`) still rely on the shared
   key for incremental pull. Deprecation is safe only after `team_ids` migration is
   complete.

4. **Engine reads `last_sync` using `ConfigPrefix() + ".last_sync"` as a single key
   today.** Multi-team sync requires the engine to call `store.GetConfig` with the
   per-team key `linear.<uuid>.last_sync` for each team separately. The engine's
   `ConfigPrefix()` pattern does not extend cleanly to multi-team; the fan-out loop must
   construct the key explicitly.

5. **`external_ref` URL format is a stable contract.** The push routing logic depends on
   parsing `<IDENTIFIER>` from `https://linear.app/<workspace>/issue/<IDENTIFIER>` and
   matching it to a configured team UUID via the cached `team_key` prefix. Changing the
   `external_ref` format would break this routing without a migration.

## Open Questions

1. **Should `linear.<uuid>.team_key` be written at config-set time or lazily at first
   sync?** Lazy population means the first push after adding a second team requires an
   extra `GET /teams` API call. Config-set time is cleaner but requires `bd config set
   linear.team_ids` to call the Linear API (or expose a separate `bd linear sync --setup`
   step). **Recommendation:** lazy, with a one-time population on first sync per team.

2. **Per-team project_id naming:** `linear.<uuid>.project_id` is unambiguous but verbose.
   Alternative: `linear.project_id.<uuid>` to match the `linear.priority_map.<N>` convention.
   **Recommendation:** `linear.<uuid>.project_id` to group all per-team settings under the
   UUID namespace rather than by field name.

3. **`linear.team_id` deprecation timeline:** When should `linear.team_id` stop being
   written by `bd config set linear.team_id`? v1 should still write it for single-team
   users. v2 deprecation could emit a notice. **Leave this decision to post-v1.**

4. **Orphan key cleanup:** Removing a UUID from `linear.team_ids` leaves orphaned
   `linear.<uuid>.*` keys in the config table. Is silent retention acceptable or should
   removal warn/clean up? **Recommendation:** Retain silently in v1; document as a known
   limitation.

## Integration Points

**API / Interface dimension:**
- `bd config set linear.team_ids "UUID1,UUID2"` is the primary write path. The config
  command must document that this is a full-replacement write (not append). A
  `bd config append` convenience command is out of scope for v1 but should be noted as
  a UX gap.
- `validateLinearConfig()` must be updated to accept `linear.team_ids` OR `linear.team_id`
  and produce an actionable error if neither is set.
- `bd linear status` output must iterate `team_ids` and display per-team `last_sync` and
  issue counts.

**Integration / Engine dimension:**
- The engine's fan-out loop reads `linear.team_ids`, constructs a new `Tracker` per UUID,
  and calls `store.GetConfig(ctx, "linear."+uuid+".last_sync")` for incremental pull.
- After each per-team sync, the engine writes `linear.<uuid>.last_sync`.
- The shared `linear.last_sync` key should be written only when team count is 1 (for
  single-team backward compat).

**Security dimension:**
- No new secrets. `linear.api_key` is shared across all teams (one key per workspace).
- Per-team keys store UUIDs, timestamps, and project IDs — no sensitive data beyond what
  already exists.

**Scalability dimension:**
- `GetAllConfig()` returns all config rows. For 10 teams × ~5 keys/team = ~50 new rows.
  Negligible impact. Not a scalability concern.
