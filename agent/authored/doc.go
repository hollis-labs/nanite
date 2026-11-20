// Package authored validates the Nanite-specific files an agent directory
// may hold beside the libs agentdir files: subtask variants
// (variants/<name>.md) and providers.yaml. The engine loads them through
// these functions, so a tool that edits an agent directory (an editor, a
// workbench, CI) applies exactly the rules Nanite will.
//
// The parse functions take bytes, for content not yet written; the load
// functions read an agent directory. None of them resolve keys, contact a
// provider or start anything.
package authored
