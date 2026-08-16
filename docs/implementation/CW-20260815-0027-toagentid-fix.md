# ToAgentID Identity Resolution Fix (CW-20260815-0027)

## Summary

This fix addresses the ToAgentID identity-resolution gap disclosed in CW-20260815-0023. It mirrors the FromAgentID fix pattern to ensure subagent reply messages can be delivered successfully when the parent agent is identified by a role slug instead of a real agent profile ID.

## Problem

When `run.ParentAgentID` contains a role slug (e.g., "operator") instead of a database UUID or file-based ID (e.g., "agt-operator-001"), the subagent reply-delivery code would pass this slug as `ToAgentID` in the messaging layer. 

While this doesn't cause the same auto-register collision that FromAgentID had (ToAgentID doesn't trigger auto-register), it causes `ValidateAgentID` to fail because:
1. The resolver looks for a row with `ID = "operator"`
2. No such row exists (the real row has `ID = "agt-operator-001"` and `slug = "operator"`)
3. The message send fails with "agent not found: operator"
4. The parent session never receives the subagent's completion result

This failure mode is silent (only logged as a warning), so the parent agent loses track of dispatched work.

## Solution

Added `replyToAgentID()` method in `internal/subagent/service_toagent_fix.go` that:
1. Attempts to resolve the parentAgentID as a slug via `ProfileResolver.GetAgentBySlug()`
2. Returns the resolved profile ID when found
3. Falls back to the original value when resolution isn't available or fails

This mirrors the `replyFromAgentID()` pattern from CW-20260815-0023.

## Files Changed

### New Files
- `internal/subagent/service_toagent_fix.go` - Contains `replyToAgentID()` method
- `internal/subagent/service_toagent_test.go` - Regression test

### Modified Files
- `internal/subagent/service.go` - Updated two locations where `ToAgentID` is set:
  1. Line ~975: Rejection message delivery
  2. Line ~1302: Completion result delivery

## Testing

### New Test
`TestSpawn_ReplyDelivery_ParentSlug_ToAgentIDResolution` verifies:
1. A spawn with `ParentAgentID="operator"` (slug) completes successfully
2. The reply message is delivered to the parent session
3. The message's `ToAgentID` field contains the resolved ID ("agt-operator-001"), not the slug
4. The `replyToAgentID()` function correctly resolves slugs and passes through valid IDs

### Test Results
```
✓ TestSpawn_ReplyDelivery_ParentSlug_ToAgentIDResolution (0.19s)
✓ TestSpawn_ReplyDelivery_ExistingRoleSlug_NoAutoRegisterCollision (0.12s)
✓ All subagent tests pass (10.629s total)
```

## Verification Commands

Build check:
```bash
go vet ./internal/subagent
```

Run specific test:
```bash
go test -v -run TestSpawn_ReplyDelivery_ParentSlug_ToAgentIDResolution ./internal/subagent
```

Run all subagent tests:
```bash
go test ./internal/subagent
```

## Related Work

- **CW-20260815-0023**: Original FromAgentID fix (the reference pattern for this fix)
- **CW-20260516-0058**: ParentAgentID context override to prevent LLM from supplying wrong IDs
- **CW-20260512-0019**: Subagent result message kind distinction

## Impact

This fix ensures that subagent replies are delivered successfully regardless of whether the parent agent is identified by:
- A database UUID (e.g., "agt-operator-001")
- A role slug (e.g., "operator")
- A file-based ID (e.g., "file-operator")

The fallback behavior maintains backward compatibility with test paths and direct invocations that may not wire a ProfileResolver.
