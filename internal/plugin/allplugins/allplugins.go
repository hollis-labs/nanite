// Package allplugins imports all built-in plugin packages for their init()
// side effects, which register constructors in the plugin registry.
//
// Import this package (with a blank identifier) in main to enable
// auto-discovery of all compiled-in plugins:
//
//	import _ "github.com/hollis-labs/nanite/internal/plugin/allplugins"
package allplugins

import (
	_ "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-nanite-native"
	_ "github.com/hollis-labs/nanite/internal/plugin/builtin/agentwidgets"
	_ "github.com/hollis-labs/nanite/internal/plugin/builtin/bookmarks"
	_ "github.com/hollis-labs/nanite/internal/plugin/builtin/contextwidgets"
	_ "github.com/hollis-labs/nanite/internal/plugin/builtin/debugwidgets"
	_ "github.com/hollis-labs/nanite/internal/plugin/builtin/giphy"
	_ "github.com/hollis-labs/nanite/internal/plugin/builtin/observabilitywidgets"
	_ "github.com/hollis-labs/nanite/internal/plugin/builtin/oembed"
	_ "github.com/hollis-labs/nanite/internal/plugin/builtin/sessionstats"
	_ "github.com/hollis-labs/nanite/plugins/fragments-engine"
	_ "github.com/hollis-labs/nanite/plugins/support-ticket"
)
