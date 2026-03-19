// Package allplugins imports all built-in plugin packages for their init()
// side effects, which register constructors in the plugin registry.
//
// Import this package (with a blank identifier) in main to enable
// auto-discovery of all compiled-in plugins:
//
//	import _ "github.com/hollis-labs/conduit/internal/plugin/allplugins"
package allplugins

import (
	_ "github.com/hollis-labs/conduit/plugins/session-stats"
	_ "github.com/hollis-labs/conduit/plugins/support"
)
