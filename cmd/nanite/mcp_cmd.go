package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/mcpconfig"
	"github.com/hollis-labs/nanite/internal/store"
)

// mcpImport reads a .mcp.json file and creates DB records for each server.
func mcpImport(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: %s mcp import <path> [--db <path>]\n", brand.BinaryName)
		os.Exit(1)
	}

	filePath := args[0]
	dbPath := resolveDBPath()

	// Parse optional --db flag from remaining args.
	for i := 1; i < len(args)-1; i++ {
		if args[i] == "--db" {
			dbPath = args[i+1]
			break
		}
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: read %s: %v\n", filePath, err)
		os.Exit(1)
	}

	s, err := store.New(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open db: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	result, err := mcpconfig.Import(s, data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: import: %v\n", err)
		os.Exit(1)
	}

	for _, name := range result.Created {
		fmt.Printf("  created: %s\n", name)
	}
	for _, name := range result.Skipped {
		fmt.Printf("  skipped (exists): %s\n", name)
	}
	fmt.Printf("\n%d created, %d skipped\n", len(result.Created), len(result.Skipped))
}

// mcpExport writes all DB MCP server configs as a .mcp.json file.
func mcpExport(args []string) {
	dbPath := resolveDBPath()
	outPath := ""

	// Parse args: [path] [--db <path>]
	for i := 0; i < len(args); i++ {
		if args[i] == "--db" && i+1 < len(args) {
			dbPath = args[i+1]
			i++
		} else if outPath == "" {
			outPath = args[i]
		}
	}

	s, err := store.New(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open db: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	cfg, err := mcpconfig.Export(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: export: %v\n", err)
		os.Exit(1)
	}

	data, err := mcpconfig.Marshal(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: marshal: %v\n", err)
		os.Exit(1)
	}
	data = append(data, '\n')

	if outPath == "" {
		os.Stdout.Write(data)
		return
	}

	// Ensure parent directory exists.
	if dir := filepath.Dir(outPath); dir != "." {
		os.MkdirAll(dir, 0755)
	}

	if err := os.WriteFile(outPath, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error: write %s: %v\n", outPath, err)
		os.Exit(1)
	}
	fmt.Printf("Exported %d server(s) to %s\n", len(cfg.MCPServers), outPath)
}
