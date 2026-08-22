package skill

// load.go — TASKS/skills/12
// (TASKS/skills/12-remaining-skills-rest-api-list-grants-preview-uninstall.md):
// LoadRootDefinition is extracted from internal/selftools/
// self_tools_skill_get.go's original, unexported loadRootSkillDefinition
// (TASKS/skills/11) so the API-direct skill_get self-tool and this task's
// own POST /api/skills/{slug}/preview REST endpoint share one
// implementation for "re-read a skill's vendored package and re-parse its
// SKILL.md," rather than maintaining two independent copies of the same
// three-call sequence. Behavior is unchanged from task 11's original.
//
// "Materialization always reads the vendored copy live, every time a
// skill is used" per docs/engineering/architecture/20-skills.md's "The
// model" section — this is the shared entry point every root-skill
// materialization call (skill_get, preview) starts from, never a value
// cached at install time.
import (
	"fmt"

	"github.com/hollis-labs/nanite/internal/skillvendor"
	"github.com/hollis-labs/nanite/internal/store"
)

// LoadRootDefinition reads sk's vendored package (keyed by sk.ContentHash)
// and re-parses its SKILL.md into a Definition. def.Slug falls back to
// sk.Slug when the re-parsed frontmatter is somehow empty (should not
// happen for an installed package, since install-time validation requires
// a resolvable slug, but ParseMD alone has no filename fallback to fall
// back on the way ParseMDFile/ParsePackageDir do for a bare byte slice);
// def.SourceRef is stamped to sk.ContentHash so downstream provenance/
// composition code can tell which vendored address a given Definition
// came from.
func LoadRootDefinition(vendor *skillvendor.Store, sk *store.Skill) (def *Definition, pkgDir string, err error) {
	pkgDir, err = vendor.Path(sk.ContentHash)
	if err != nil {
		return nil, "", fmt.Errorf("resolve vendored package path for %q: %w", sk.Slug, err)
	}
	files, err := vendor.ReadFiles(sk.ContentHash)
	if err != nil {
		return nil, "", fmt.Errorf("read vendored package for %q: %w", sk.Slug, err)
	}
	data, ok := files[skillFileName]
	if !ok {
		return nil, "", fmt.Errorf("vendored package for %q has no %s", sk.Slug, skillFileName)
	}
	def, err = ParseMD(data)
	if err != nil {
		return nil, "", fmt.Errorf("parse %s for %q: %w", skillFileName, sk.Slug, err)
	}
	if def.Slug == "" {
		def.Slug = sk.Slug
	}
	def.SourceRef = sk.ContentHash
	return def, pkgDir, nil
}
