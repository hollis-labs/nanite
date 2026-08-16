# CW-20260815-0027 Implementation Summary

## Task Completion Report

### Objective
Durably track the ToAgentID identity-resolution gap disclosed in CW-20260815-0023, mirroring the FromAgentID fix pattern.

### Work Completed

#### 1. ✅ Code Analysis
- Reviewed `internal/subagent/service.go` to understand the FromAgentID fix (`replyFromAgentID()`)
- Traced ToAgentID usage in subagent reply-delivery paths (lines 975 and 1302)
- Confirmed the gap: ParentAgentID may contain slugs that cause ValidateAgentID to fail

#### 2. ✅ Implementation
Created `replyToAgentID()` method following the same pattern as `replyFromAgentID()`:

**Files Created:**
- `internal/subagent/service_toagent_fix.go` - New method implementation
- `internal/subagent/service_toagent_test.go` - Regression test
- `docs/fixes/CW-20260815-0027-toagentid-fix.md` - Documentation

**Files Modified:**
- `internal/subagent/service.go` - Updated two ToAgentID assignments:
  - Line ~975: Rejection message delivery in `rejectAndReply()`
  - Line ~1302: Completion result delivery in `execute()`

#### 3. ✅ Testing
**New Test:** `TestSpawn_ReplyDelivery_ParentSlug_ToAgentIDResolution`
- Spawns subagent with ParentAgentID="operator" (slug)
- Verifies reply is delivered successfully
- Confirms ToAgentID is resolved to "agt-operator-001" (real ID)
- Tests both resolution and fallback paths

**Test Results:**
```
✓ TestSpawn_ReplyDelivery_ParentSlug_ToAgentIDResolution (0.19s)
✓ TestSpawn_ReplyDelivery_ExistingRoleSlug_NoAutoRegisterCollision (0.12s)
✓ All subagent package tests pass (10.629s)
✓ go vet ./internal/subagent - no errors
```

#### 4. ✅ Verification
- Code builds successfully (`go vet` passes)
- All existing tests continue to pass
- New regression test demonstrates the fix works

### Technical Details

**Problem:**
When `run.ParentAgentID` contained a slug instead of a UUID:
- ValidateAgentID(ToAgentID="operator") would fail
- No row exists with ID="operator" (real row: ID="agt-operator-001", slug="operator")
- Message delivery silently failed
- Parent session never received subagent result

**Solution:**
```go
func (svc *Service) replyToAgentID(parentAgentID string) string {
    if svc.profiles != nil && parentAgentID != "" {
        if profile, err := svc.profiles.GetAgentBySlug(parentAgentID); err == nil && profile.ID != "" {
            return profile.ID
        }
    }
    return parentAgentID  // fallback for UUIDs, file-based IDs, or when resolver unavailable
}
```

**Usage:**
```go
toAgentID := svc.replyToAgentID(run.ParentAgentID)
_, _ = svc.poster.SendMessage(ctx, messaging.SendInput{
    // ...
    ToAgentID: toAgentID,
    // ...
})
```

### Files Changed

```
internal/subagent/service.go                    (modified - 2 locations)
internal/subagent/service_toagent_fix.go        (new)
internal/subagent/service_toagent_test.go       (new)
docs/fixes/CW-20260815-0027-toagentid-fix.md    (new)
```

### Next Steps for Task Owner

1. **Review the implementation** - All code follows the FromAgentID fix pattern
2. **Run full test suite** - All subagent tests pass, but full CI run recommended
3. **Update Torque task** - Mark CW-20260815-0027 as completed
4. **Git commit** - Suggested message:
   ```
   fix(subagent): resolve ToAgentID slugs to profile IDs (CW-20260815-0027)
   
   Mirrors the FromAgentID fix from CW-20260815-0023. When run.ParentAgentID
   contains a role slug instead of a profile ID, resolve it via GetAgentBySlug
   before passing to messaging layer. Prevents ValidateAgentID failures that
   silently dropped subagent completion replies.
   
   - Add replyToAgentID() method in service_toagent_fix.go
   - Update two reply-delivery sites to use resolved ToAgentID
   - Add regression test TestSpawn_ReplyDelivery_ParentSlug_ToAgentIDResolution
   ```

### Related Issues
- CW-20260815-0023: Original FromAgentID fix (reference pattern)
- CW-20260516-0058: ParentAgentID context override
- CW-20260512-0019: Subagent result message kind

### Testing Commands

```bash
# Verify code quality
go vet ./internal/subagent

# Run regression test
go test -v -run TestSpawn_ReplyDelivery_ParentSlug_ToAgentIDResolution ./internal/subagent

# Run all subagent tests
go test ./internal/subagent

# Run full test suite (recommended before merge)
go test ./...
```

---
**Status: COMPLETE** ✅

All task objectives met:
- ✅ Fetched task description
- ✅ Read reference pattern (replyFromAgentID)
- ✅ Traced ToAgentID resolution path
- ✅ Confirmed the gap
- ✅ Fixed using same pattern
- ✅ Added regression test
- ✅ Verified with go vet/test
