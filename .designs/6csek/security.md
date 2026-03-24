# Security Analysis

## Summary

The Linear multi-team sync feature extends beads to fan out sync operations across multiple
Linear teams using a single workspace API key. The security posture is largely unchanged
from single-team mode: there are no new authentication mechanisms, no new network protocols,
and no new external services. The primary risks introduced are (1) API key scope amplification—
one key now gates access to more data paths—and (2) a new attack surface on the config
key namespace where adversarial team UUIDs or malformed `linear.team_ids` values could
cause misbehavior.

The codebase already handles the highest-risk concern (SQL injection via config keys)
correctly: the storage layer uses parameterized queries throughout
(`internal/storage/issueops/config_metadata.go`). The remaining risks are low-severity
and largely mitigatable with input validation at config-read time.

## Analysis

### Key Considerations

- **One API key, N teams.** The `linear.api_key` is a workspace-scoped credential shared
  across all configured teams. A compromised key exposes all teams, not just one. This
  was already true in spirit (workspace tokens grant full workspace access), but multi-team
  makes the operational blast radius explicit.
- **Config key construction via string interpolation.** Fan-out code will construct keys
  like `"linear." + uuid + ".last_sync"`. If a malicious UUID contains a dot or special
  character (e.g., `foo.team_key`), it could shadow an unrelated config key. UUID
  validation (`isValidUUID()`) is already present in the CLI layer but must be applied at
  engine fan-out time as well.
- **`linear.api_endpoint` is an SSRF vector.** A user (or a compromised `.beads/config`
  store) can redirect all Linear API traffic to an arbitrary URL, including internal
  addresses. The API key is sent in the `Authorization` header to whatever endpoint is
  configured. This is an existing risk that multi-team amplifies: N team syncs = N
  requests to a potentially adversarial endpoint per cycle.
- **`linear.team_ids` parse integrity.** The CSV parser for `linear.team_ids` must not
  accept values that could cause the fan-out loop to silently skip teams or construct
  invalid per-team keys. Empty strings after split/trim, non-UUID values, or duplicates
  are all edge cases that need defined behavior.
- **API key storage in Dolt.** `linear.api_key` is stored in the Dolt `config` table
  in plaintext. This is unchanged from single-team. Access requires filesystem access
  to the `.beads/` directory or Dolt server access on port 3307. Within the beads threat
  model (local-first, single-user), this is acceptable. Multi-team does not change this.
- **push routing via identifier prefix.** Push routing will match an issue's
  `external_ref` URL pattern to a cached `linear.<uuid>.team_key`. A collision between
  two team key prefixes (e.g., "ENG" and "ENGR") would misroute pushes. This is a
  correctness issue with security implications (issues pushed to wrong team).
- **Rate limiting under fan-out.** N teams × (pull + push) = 2N API calls per sync cycle.
  A misconfigured large team list could trigger Linear's rate limiter (4000 req/hour for
  personal keys), causing sync failures or exponential backoff storms. Not a security
  risk per se, but worth noting as a denial-of-service risk against the Linear API.
- **No secrets in per-team keys.** Per-team config keys (`linear.<uuid>.last_sync`,
  `linear.<uuid>.team_key`, `linear.<uuid>.project_id`) contain timestamps, identifier
  strings, and project UUIDs—no secrets. Even if exposed, they are not sensitive.

### Options Explored

#### Option 1: Validate UUIDs at fan-out time (no format changes)

**Description:** When reading `linear.team_ids` to build the fan-out list, apply the
existing `isValidUUID()` check to each element. Skip and warn on invalid entries. Key
construction proceeds only with validated UUIDs.

- **Pros:** Minimal change. Reuses existing validation logic. Prevents key shadowing from
  malformed UUIDs. Zero user-visible impact for well-formed config.
- **Cons:** Doesn't prevent a malicious actor who already has write access to the config
  store from causing issues through other config keys. Not a strong security boundary.
- **Effort:** Low

#### Option 2: Reject `linear.api_endpoint` in multi-team mode

**Description:** When `linear.team_ids` has more than one entry, disallow custom
`linear.api_endpoint` configuration. Multi-team mode requires the production Linear API.

- **Pros:** Eliminates SSRF amplification in multi-team mode.
- **Cons:** Breaks legitimate use cases (self-hosted Linear, test environments). Over-broad.
  Doesn't address the root SSRF risk, which exists in single-team mode too.
- **Effort:** Low (but wrong trade-off)

#### Option 3: Allowlist `linear.api_endpoint` to `*.linear.app`

**Description:** Validate that `linear.api_endpoint`, if set, points to a `linear.app`
subdomain or `localhost` (for tests). Reject other endpoints at config-read time.

- **Pros:** Eliminates SSRF entirely. Doesn't break the common test/mock-server use case.
  Applies uniformly to single-team and multi-team.
- **Cons:** Requires URL parsing and allowlist maintenance. Could break hypothetical
  future self-hosted Linear deployments.
- **Effort:** Low-Medium

#### Option 4: Normalize and deduplicate `linear.team_ids` on write

**Description:** At `bd config set linear.team_ids` time, parse the CSV, validate each
UUID, deduplicate, re-serialize to a canonical form, and store that. Reject the write if
any element is invalid.

- **Pros:** Prevents malformed-UUID-in-key attack at the source. Config store always
  contains a valid, canonical value. Easier to reason about downstream.
- **Cons:** `bd config set` becomes a validation gate (currently it is not). Requires
  the CLI layer to know about Linear-specific validation rules.
- **Effort:** Low

### Recommendation

**Option 1 (UUID validation at fan-out) is required and low-risk.** Implement it.

**Option 4 (normalize on write) is a good companion hardening step** and follows the
existing pattern (`issue_prefix` normalization already strips trailing hyphens at write
time in `SetConfigInTx`).

**Option 3 (allowlist endpoint) is the right long-term fix** for the SSRF risk but is
pre-existing, not multi-team-specific, and should be tracked as a separate issue rather
than blocking this feature. File it.

The combined v1 security approach:
1. Validate UUIDs when reading `linear.team_ids` in the fan-out loop (fail/warn, not panic).
2. Normalize `linear.team_ids` at write time (sort + deduplicate + UUID validation → reject on invalid).
3. Track `linear.api_endpoint` allowlisting as a follow-up issue.

## Constraints Identified

1. **SQL injection is NOT a risk.** The storage layer uses parameterized queries for all
   config reads/writes (`REPLACE INTO config (key, value) VALUES (?, ?)`). Config keys
   constructed from team UUIDs never touch SQL directly.

2. **Existing `isValidUUID()` must be called at engine fan-out time, not just at CLI input
   time.** The fan-out loop in the sync engine constructs per-team config keys directly.
   If validation is only in the CLI, a programmatic or test path that bypasses the CLI
   can write invalid UUIDs to the store.

3. **`linear.api_key` is plaintext in the Dolt store.** This is a pre-existing constraint.
   No change required for v1, but it is the highest-value target in the config table.
   Access requires Dolt server access or filesystem access to `.beads/`.

4. **API key is workspace-scoped, not team-scoped.** Linear does not issue per-team API
   keys. Multi-team mode cannot reduce the blast radius of a compromised key at the
   Linear API level. Mitigation is operational: rotate promptly, use read-only keys when
   Linear supports them.

5. **`maskAPIKey()` must be used whenever the key is displayed.** The existing
   `bd linear status` command correctly uses `maskAPIKey()`. Any new multi-team status
   output paths must do the same.

## Open Questions

1. **Should invalid UUIDs in `linear.team_ids` be hard errors (block sync) or warnings
   (skip the bad entry)?** Recommendation: warn and skip, so a single corrupted entry
   doesn't silence all team syncs.

2. **Should duplicate team UUIDs in `linear.team_ids` cause a warning at sync time, or
   only at config-set time?** Recommendation: normalize at write, warn at read if
   duplicates somehow persist (defensive).

3. **What is the expected behavior if `linear.api_key` is revoked while multiple teams
   are mid-sync?** The engine will get a 401 from Linear on the first team and fail.
   Per-team cursor state for teams that completed before the revocation is valid. The
   sync will resume correctly after the key is rotated. This is a correctness note, not
   a security gap, but error messaging should distinguish 401 from other errors.

4. **Should removing a UUID from `linear.team_ids` trigger a warning about orphaned
   per-team config keys containing the team_key identifier prefix?** Leaving orphaned
   `linear.<uuid>.team_key` entries does not create a security risk, but could cause
   confusion if a new team is assigned the same UUID (extremely unlikely). Recommend
   noting in docs as a known limitation.

5. **Is the `linear.api_endpoint` SSRF risk in scope for this feature?** Tracking as a
   pre-existing issue. Multi-team amplifies it (N requests vs 1) but does not introduce
   it. Recommend filing a follow-up bead with `security` label.

## Integration Points

**Data Model dimension:**
The UUID-namespaced key scheme (`linear.<uuid>.*`) is the foundation of this analysis.
No new secret storage is needed. API key sharing across teams is a design choice, not
a gap. The constraint analysis above (SQL parameterized queries, no schema change) is
confirmed in `internal/storage/issueops/config_metadata.go` and `internal/storage/dolt/config.go`.

**API / Interface dimension:**
- `bd config set linear.team_ids` is the write path for multi-team UUIDs. Input
  validation (UUID format check, deduplication, max-team-count) should happen here.
- `bd linear status` must mask `linear.api_key` in all output paths. New per-team
  status fields (cursor timestamp, project_id, team_key) are non-sensitive.
- Error messages must not echo raw UUID values from config in a way that exposes internal
  namespace structure to untrusted output sinks. (Low risk given local-first model.)

**Integration / Engine dimension:**
- The fan-out loop constructs config keys by string concatenation. UUID validation must
  gate this construction, not just validate at CLI input time.
- The `WithEndpoint()` method on the Linear client is the SSRF vector. It should be
  passed only after the endpoint is validated against an allowlist.

**Scalability dimension:**
Rate-limit handling under N-team fan-out is relevant here: the exponential backoff in
`client.Execute()` is per-request, not per-team. A large team count could cause the sync
to hold a database transaction open across many retries. This intersects with the
scalability analysis but has a security-adjacent DoS dimension.
