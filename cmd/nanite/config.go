package main

import (
	"errors"
	"os"
	"path/filepath"
)

// useConfigFile keeps an explicit SUBSTRATE_CONFIG, otherwise reads the app's
// optional XDG config. It never creates a file or depends on the checkout.
// Called once at process entry, before Substrate reads its override.
func useConfigFile(skip bool) (func(), error) {
	if skip {
		return overrideConfig("")
	}
	noop := func() {}
	if os.Getenv("SUBSTRATE_CONFIG") != "" {
		return noop, nil
	}
	if path := os.Getenv("NANITE_CONFIG"); path != "" {
		return overrideConfig(path)
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".config")
	}
	path, err := filepath.Abs(filepath.Join(dir, substrateApp, "nanite.yaml"))
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) { // #nosec G703 -- The operator selects this local configuration path.
		return noop, nil
	} else if err != nil {
		return nil, err
	}
	return overrideConfig(path)
}

func overrideConfig(path string) (func(), error) {
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
