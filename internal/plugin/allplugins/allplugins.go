// Package allplugins imports all built-in plugin packages for their init()
// side effects, which register constructors in the plugin registry.
//
// Import this package (with a blank identifier) in main to enable
// auto-discovery of all compiled-in plugins:
//
//	import _ "github.com/hollis-labs/conduit/internal/plugin/allplugins"
package allplugins

import (
	_ "github.com/hollis-labs/conduit/internal/plugin/builtin/agentrc"
	_ "github.com/hollis-labs/conduit/internal/plugin/builtin/agentwidgets"
	_ "github.com/hollis-labs/conduit/internal/plugin/builtin/bookmarkswidget"
	_ "github.com/hollis-labs/conduit/internal/plugin/builtin/contextwidgets"
	_ "github.com/hollis-labs/conduit/internal/plugin/builtin/demopresenter"
	_ "github.com/hollis-labs/conduit/internal/plugin/builtin/giphy"
	_ "github.com/hollis-labs/conduit/internal/plugin/builtin/marvel"
	_ "github.com/hollis-labs/conduit/internal/plugin/builtin/observabilitywidgets"
	_ "github.com/hollis-labs/conduit/internal/plugin/builtin/oembed"
	_ "github.com/hollis-labs/conduit/internal/plugin/builtin/sessionstats"
	_ "github.com/hollis-labs/conduit/plugins/support-ticket"
)
