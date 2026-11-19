// Package agent embeds a native Nanite agent in a Go application. Open reads the
// authored directory and holds its exclusive data lock until Close. The agent
// reloads its authored inputs for each turn.
//
// A host can contribute context and tools before starting a turn:
//
//	a, err := agent.Open(ctx, agent.Options{Dir: "agents/notes"})
//	if err != nil { return err }
//	defer a.Close()
//	writer := a.Slots().Owner("notes")
//	if err := writer.Put(agent.SlotWorkspace, agent.SlotEntry{
//	    Key: "active", Content: "Open notebook: Project notes",
//	}); err != nil { return err }
//	if err := a.Tools().SetGroup("notes", lookupTool); err != nil {
//	    return err
//	}
//	conversation, err := a.NewConversation(ctx)
//	if err != nil { return err }
//	run, err := conversation.Send(ctx, agent.Input{Text: "Summarize this notebook."})
//	if err != nil { return err }
//	for event := range run.Events() {
//	    if delta, ok := event.(agent.TextDelta); ok {
//	        fmt.Print(delta.Text)
//	    }
//	}
//	_, err = run.Wait()
//	return err
package agent
