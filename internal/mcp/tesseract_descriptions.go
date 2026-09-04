package mcp

// TesseractToolDescriptions contains chat-oriented overrides for Tesseract
// v0.9 tools. This registry does not expose or authorize tools.
var TesseractToolDescriptions = map[string]string{
	"tesseract_recall": tesseractRecallChatSurfaceDescription,
}

// TesseractToolRelations supplies progressive-discovery cross references.
var TesseractToolRelations = map[string]struct {
	RelatedTools  []string
	RelatedSkills []string
}{
	"tesseract_recall": {
		RelatedTools: []string{
			"tesseract_get_revision",
			"tesseract_touch",
			"tesseract_get",
			"memory_write",
			"knowledge_write",
			"tool_describe",
		},
		RelatedSkills: []string{"end-of-session"},
	},
}

const tesseractRecallChatSurfaceDescription = "Retrieve ranked memory and knowledge revisions from Tesseract v0.9. " +
	"Use targeted recall when durable context could resolve a current question; do not sweep it as a precondition for ordinary work.\n\n" +
	"**Contract:** `namespaces` is required and is a JSON-array string. Memory writes use typed namespaces such as " +
	"`user/<id>/memory/decisions`; the flat `user/<id>/memory` form is a read prefix across types. Optional `domains` is a " +
	"JSON-array string containing `memory` and/or `knowledge`. `ranking` is activation|chronological|similarity|relevance; " +
	"`search_mode` is hybrid|lexical|semantic and only selects a relevance arm. `payload_mode` is keys|summary|full and defaults " +
	"to summary. `limit`, `cursor`, `budget_bytes`, `budget_tokens`, and `estimate_only` bound progressive reads.\n\n" +
	"**Output shape:** `{results, facets, manifest}`. `manifest` always reports totals, bytes/tokens, truncation, and nullable " +
	"`next_cursor`; estimate-only omits results. `score` is nullable: chronological and lexical relevance results have no numeric " +
	"score, while semantic scores may legitimately be zero or negative. Projected rows carry `payload_mode`; a missing body under " +
	"keys/summary is withheld, not empty. Recall summaries, choose a few revision IDs, hydrate them with " +
	"`tesseract_get_revision`, then call `tesseract_touch` only for entries actually used.\n\n" +
	"**Golden example:** `{namespaces: \"[\\\"user/alice/memory\\\"]\", domains: \"[\\\"memory\\\"]\", " +
	"query: \"deployment rollback decision\", ranking: \"relevance\", search_mode: \"lexical\", payload_mode: \"summary\", limit: 5}`."
