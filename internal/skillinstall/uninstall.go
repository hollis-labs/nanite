package skillinstall

// uninstall.go — TASKS/skills/12
// (TASKS/skills/12-remaining-skills-rest-api-list-grants-preview-uninstall.md):
// the reverse of Installer (install.go) — docs/engineering/architecture/
// 20-skills.md's "API surface" section: "Delete/uninstall a skill from the
// vendored store and index." Both the DELETE /api/skills/{slug} REST
// handler (internal/api/skills.go) and the skill_delete self-tool
// (internal/selftools/self_tools_transport.go) construct a fresh
// Uninstaller per call and share this exact implementation, satisfying
// this task's own Done-means ("skill_delete and DELETE /api/skills/{slug}
// both call the same uninstall logic").
//
// A deliberately separate pair of narrow interfaces from Installer's own
// Vendorer/IndexStore (install.go), rather than extending those directly:
// install_test.go's fakeIndex (a test double satisfying IndexStore for
// Installer's own failure-path coverage) implements only GetSkillBySlug/
// CreateSkill/UpdateSkill — adding a DeleteSkill method to IndexStore would
// force that unrelated test fixture to grow an unused method it has no
// reason to implement.
//
// Uninstall takes an already-resolved *store.Skill rather than a slug/ID
// string: callers on this batch's two surfaces resolve a skill by whatever
// identifier convention their own surface exposes (REST: slug-primary,
// ID-fallback, per internal/api/skills.go's resolveSkillRef; the self-tool:
// slug-or-id, matching its own existing "id" argument plus this task's
// slug addition) using an ordinary, already-existing store lookup — there
// is no real "logic" in that resolution step worth centralizing here. What
// both callers must share, and get identically right, is what happens
// once a specific row is confirmed: delete the vendored copy (when one
// exists) before removing the index row, never the other order.
import (
	"context"
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

// UninstallIndexStore is the narrow slice of *store.Store's API Uninstall
// depends on to remove a skill's index row once resolved.
type UninstallIndexStore interface {
	DeleteSkill(ctx context.Context, id string) error
}

// UninstallVendorer is the narrow slice of *internal/skillvendor.(*Store)'s
// API Uninstall depends on to remove a skill's vendored copy.
type UninstallVendorer interface {
	Delete(address string) error
}

// UninstallResult is what a successful Uninstall call returns.
type UninstallResult struct {
	// Skill is the index row that was removed (its pre-deletion values —
	// there is nothing left in the index to re-fetch afterward).
	Skill store.Skill
	// VendorDeleted reports whether a vendored copy was actually deleted —
	// false for a skill whose index row had never been through an
	// install/sync (ContentHash empty, e.g. a bare admin-CRUD row created
	// directly via POST /api/skills, task 02's own pre-install surface),
	// in which case there is nothing in the vendored store to remove.
	VendorDeleted bool
}

// Uninstaller performs the real uninstall: remove a skill's vendored copy
// (when it has one) and its skills index row. Construct fresh per call,
// matching this batch's established "build fresh per invocation"
// convention for install-adjacent pipeline objects (Installer's own doc
// comment; service.Container.SkillVendor's).
type Uninstaller struct {
	Vendor UninstallVendorer
	Index  UninstallIndexStore
}

// Uninstall removes sk's vendored copy (when sk.ContentHash is non-empty)
// and then its skills index row.
//
// Vendor deletion runs BEFORE the index row is removed, and a vendor
// deletion failure aborts the whole call with the index row still intact:
// the index row is the only durable pointer to sk.ContentHash's vendored
// directory, so deleting the index row first and then failing to delete
// the vendored copy would leave an orphaned, un-addressable vendored
// directory with nothing left in the system that could ever identify or
// clean it up. The reverse order has no equivalent hazard — a vendored
// copy deleted just before an index-row-deletion failure is merely a
// (still fully re-installable, since the original source package is
// unaffected) content-store miss, not a permanently unrecoverable orphan.
func (u *Uninstaller) Uninstall(sk *store.Skill) (UninstallResult, error) {
	if sk == nil {
		return UninstallResult{}, errors.New("skillinstall: Uninstall: skill is nil")
	}
	if u.Index == nil {
		return UninstallResult{}, errors.New("skillinstall: Uninstaller.Index is nil")
	}

	vendorDeleted := false
	if sk.ContentHash != "" {
		if u.Vendor == nil {
			return UninstallResult{}, fmt.Errorf(
				"skillinstall: skill %q has a vendored copy (%s) but no Vendor is configured to delete it",
				sk.Slug, sk.ContentHash)
		}
		if err := u.Vendor.Delete(sk.ContentHash); err != nil {
			return UninstallResult{}, fmt.Errorf(
				"skillinstall: delete vendored copy %q for skill %q: %w", sk.ContentHash, sk.Slug, err)
		}
		vendorDeleted = true
	}

	if err := u.Index.DeleteSkill(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sk.ID); err != nil {
		return UninstallResult{}, fmt.Errorf("skillinstall: delete index row for skill %q: %w", sk.Slug, err)
	}

	return UninstallResult{Skill: *sk, VendorDeleted: vendorDeleted}, nil
}
