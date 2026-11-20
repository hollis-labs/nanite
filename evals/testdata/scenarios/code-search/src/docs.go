package app

// describeMode explains the setting to users. It mentions APP_MODE but does
// not read it.
func describeMode() string {
	return "Set APP_MODE to choose how the service runs."
}

// legacyModes reads the old plural variable, APP_MODES, which is no longer
// used for anything but a warning.
func legacyModes(getenv func(string) string) bool {
	return getenv("APP_MODES") != ""
}
