Generate a UI page using Sigil's declarative YAML system. Description: $ARGUMENTS

Design a Sigil page config based on the description. Consider: datasources (what data?), layout (rows/columns/grid/tabs), components (data-table/stat-card/form/chart), actions (navigate/modal/http), keyboard shortcuts.

If Sigil MCP tools are available (sigil_create_page, sigil_validate, sigil_generate, sigil_preview), use them. Otherwise write YAML config directly.

Ask user which render target: react-shadcn (for Conduit/React) or go-templ (for Volon/Go). Generate code, show to user for review. Iterate on the YAML config if changes needed.

49 component types available across 6 categories: primitives (heading, text, button, input...), layouts (rows, columns, grid, card, tabs...), navigation (breadcrumb, pagination...), data (data-table, stat-card, chart...), composites (modal, sheet, dropdown...), forms (form, field-group).
