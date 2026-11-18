package main

import (
	"errors"
	"os"
	"path/filepath"
)

// useConfigFile keeps an explicit SUBSTRATE_CONFIG, otherwise reads the app's
// optional XDG config. It never creates a file or depends on the checkout.
// Called once at process entry, before Substrate reads its override.
func useConfigFile() (func(), error) {
	noop := func() {}
	if os.Getenv("SUBSTRATE_CONFIG") != "" {
		return noop, nil
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".config")
	}
	path, err := filepath.Abs(filepath.Join(dir, substrateApp, "config.yaml"))
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return noop, nil
	} else if err != nil {
		return nil, err
	}
	old, had := os.LookupEnv("SUBSTRATE_CONFIG")
	if err := os.Setenv("SUBSTRATE_CONFIG", path); err != nil {
		return nil, err
	}
	return func() {
		if had {
			_ = os.Setenv("SUBSTRATE_CONFIG", old)
		} else {
			_ = os.Unsetenv("SUBSTRATE_CONFIG")
		}
	}, nil
}
