package main

import (
	"context"
	"fmt"
	"os"
)

func Example() {
	if err := demo(context.Background(), os.Stdout, "", true); err != nil {
		fmt.Println(err)
	}
	// Output:
	// Turn 1 slots: workspace changed, context changed
	// Tool: notes_search
	// The notes mention a planning meeting.
	// Turn 2 slots: workspace changed, context changed
	// Tool: files_list
	// The workspace contains agenda.md.
	// Turn 3 slots: workspace cleared, context cleared
	// The workspace context is clear.
}
