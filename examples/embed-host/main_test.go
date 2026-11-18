package main

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
)

func Example() {
	var out bytes.Buffer
	if err := demo(context.Background(), &out, "", true); err != nil {
		fmt.Println(err)
		return
	}
	// The actual count depends on the lengths of the two workspace paths.
	permissionTokens := regexp.MustCompile(`(?m)^  permissions: [0-9]+ tokens`)
	fmt.Print(permissionTokens.ReplaceAllString(out.String(), "  permissions: <varies> tokens"))
	// Output:
	// Turn 1 slots:
	//   universal: 20 tokens, changed yes
	//     engine/rules: ship
	//   agent: 19 tokens, changed yes
	//     file/instructions: ship
	//   permissions: <varies> tokens, changed yes
	//     engine/mode: ship
	//   workspace: 1 tokens, changed yes
	//     demo/view: ship
	//   context: 4 tokens, changed yes
	//     demo/selection: ship
	// Tools offered: ask_user, background_cancel, background_start, background_status, bash, edit_file, fetch_tool_result, list_dir, notes_search, read_file, scratchpad_read, scratchpad_write, search_tool_result, skill_get, skill_list, subagent_cancel, subagent_spawn, subagent_status, todo_list, todo_write, write_file
	// Tool called: notes_search
	// The notes mention a planning meeting.
	// Turn 2 slots:
	//   universal: 20 tokens, changed no
	//     engine/rules: ship
	//   agent: 19 tokens, changed no
	//     file/instructions: ship
	//   permissions: <varies> tokens, changed no
	//     engine/mode: ship
	//   workspace: 1 tokens, changed yes
	//     demo/view: ship
	//   context: 4 tokens, changed yes
	//     demo/selection: ship
	// Tools offered: ask_user, background_cancel, background_start, background_status, bash, edit_file, fetch_tool_result, files_list, list_dir, read_file, scratchpad_read, scratchpad_write, search_tool_result, skill_get, skill_list, subagent_cancel, subagent_spawn, subagent_status, todo_list, todo_write, write_file
	// Tool called: files_list
	// The workspace contains agenda.md.
	// Turn 3 slots:
	//   universal: 20 tokens, changed no
	//     engine/rules: ship
	//   agent: 19 tokens, changed no
	//     file/instructions: ship
	//   permissions: <varies> tokens, changed no
	//     engine/mode: ship
	//   workspace: 0 tokens, changed yes
	//   context: 0 tokens, changed yes
	// Tools offered: ask_user, background_cancel, background_start, background_status, bash, edit_file, fetch_tool_result, list_dir, read_file, scratchpad_read, scratchpad_write, search_tool_result, skill_get, skill_list, subagent_cancel, subagent_spawn, subagent_status, todo_list, todo_write, write_file
	// The workspace context is clear.
}
