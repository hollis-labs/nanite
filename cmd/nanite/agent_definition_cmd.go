package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hollis-labs/nanite/internal/agentauthor"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

// runPinnedAgentCommand accepts authored v2 content. It has no old-format
// adapters, mutation-by-slug sync, enrollment or declaration-to-grant step.
func runPinnedAgentCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: nanite agent <author|validate|install> [--role <role.md>] [--resources <manifest.json>] <definition.md>")
	}
	if args[0] == "author" {
		return authorPinnedAgent(args[1:], out)
	}
	if args[0] != "validate" && args[0] != "install" {
		return errors.New("agent command requires validate or install; old profile sync/import is retired")
	}
	fs := flag.NewFlagSet("agent "+args[0], flag.ContinueOnError)
	fs.SetOutput(out)
	resourcePath := fs.String("resources", "", "explicit URI-to-local-file JSON manifest")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("one authored definition file is required")
	}
	data, err := readAuthoredDefinitionFile(fs.Arg(0))
	if err != nil {
		return err
	}
	d, err := agentdef.Parse(data, agentpolicy.Option())
	if err != nil {
		return err
	}
	digest, err := agentdef.Digest(d)
	if err != nil {
		return err
	}
	if args[0] == "validate" {
		if *resourcePath != "" {
			return errors.New("resource byte verification is performed during install; validate checks authored schema and semantic pin")
		}
		return json.NewEncoder(out).Encode(struct {
			ID       string `json:"id"`
			Revision string `json:"revision"`
			Digest   string `json:"digest"`
		}{d.DefinitionID, d.Revision, digest})
	}
	var resources []store.DefinitionResource
	if *resourcePath != "" {
		manifest, manifestErr := readAuthoredDefinitionFile(*resourcePath)
		if manifestErr != nil {
			return manifestErr
		}
		var refs []struct {
			URI  string `json:"uri"`
			Path string `json:"path"`
		}
		dec := json.NewDecoder(bytes.NewReader(manifest))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&refs); err != nil {
			return err
		}
		if dec.Decode(new(any)) != io.EOF {
			return errors.New("resource manifest must be one JSON array")
		}
		for _, ref := range refs {
			body, resourceErr := readAuthoredDefinitionFile(ref.Path)
			if resourceErr != nil {
				return resourceErr
			}
			resources = append(resources, store.DefinitionResource{URI: ref.URI, Content: body})
		}
	}
	st, err := store.New(context.Background(), resolveDBPath())
	if err != nil {
		return err
	}
	defer closeStoreBestEffort(context.Background(), st)
	pin, err := st.InstallAgentDefinition(context.Background(), data, resources)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(pin)
}

// Authoring flattens intrinsic role behavior before installation. It makes no
// resource-byte, host configuration, enrollment or grant claim.
func authorPinnedAgent(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("agent author", flag.ContinueOnError)
	fs.SetOutput(out)
	rolePath := fs.String("role", "", "authored v2 role template")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("one concrete v2 definition file is required")
	}
	data, err := readAuthoredDefinitionFile(fs.Arg(0))
	if err != nil {
		return err
	}
	concrete, err := agentdef.Parse(data, agentpolicy.Option())
	if err != nil {
		return err
	}
	var role *agentdef.Definition
	if *rolePath != "" {
		data, err = readAuthoredDefinitionFile(*rolePath)
		if err != nil {
			return err
		}
		role, err = agentdef.Parse(data, agentpolicy.Option())
		if err != nil {
			return err
		}
	}
	flat, err := agentauthor.Flatten(role, concrete)
	if err != nil {
		return err
	}
	rendered, err := agentauthor.Render(flat)
	if err != nil {
		return err
	}
	_, err = out.Write(rendered)
	return err
}

func readAuthoredDefinitionFile(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 G703 -- explicit local CLI authoring input; no server or untrusted remote path. Only bounded regular file bytes are read.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("authored input must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("authored input exceeds 1 MiB")
	}
	return data, nil
}
