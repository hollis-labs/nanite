package store

// E1 (CW-20260428-0016): the source-classification logic is the key
// affordance gate for the FE — "internal" skills are read-only by default
// and editable only in dev mode; "user"/"plugin" follow the folder-drop
// contract. The classifier is a single source of truth so the FE and the
// dev-mode editor agree on which skills can be forked.

// SkillCategory groups Source values into the three FE-relevant buckets.
// internal = built-in or empty (legacy); user = ~/.nanite/ folder-drop;
// plugin = registered by a plugin. Anything else lands in "other" — the FE
// renders it as the raw source string for diagnostic visibility.
const (
	SkillCategoryInternal = "internal"
	SkillCategoryUser     = "user"
	SkillCategoryPlugin   = "plugin"
	SkillCategoryOther    = "other"
)

// ClassifySkillSource maps a Skill.Source string to one of the four
// SkillCategory* constants above. Empty / unknown values resolve to
// internal (back-compat: pre-J7 rows had no source column).
func ClassifySkillSource(source string) string {
	switch source {
	case "", "builtin", "seed":
		return SkillCategoryInternal
	case "user", "project":
		return SkillCategoryUser
	case "plugin":
		return SkillCategoryPlugin
	}
	return SkillCategoryOther
}
