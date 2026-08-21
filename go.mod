module github.com/hollis-labs/nanite

go 1.26.2

require (
	github.com/creack/pty v1.1.24 // indirect
	github.com/dgraph-io/badger/v4 v4.9.1
	github.com/google/uuid v1.6.0
	github.com/zalando/go-keyring v0.2.8
	go.opentelemetry.io/otel v1.44.0
	go.opentelemetry.io/otel/trace v1.44.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.54.0
)

require (
	github.com/anthropics/anthropic-sdk-go v1.45.0
	github.com/google/jsonschema-go v0.4.2
	github.com/hollis-labs/agentkit v0.3.0
	github.com/hollis-labs/go-envelopes v0.1.1
	github.com/hollis-labs/go-modelsdev v0.2.0
	github.com/hollis-labs/go-otel v0.1.0
	github.com/hollis-labs/go-providers v0.23.0
	github.com/hollis-labs/go-sandbox v0.2.1
	github.com/modelcontextprotocol/go-sdk v1.5.0
	github.com/oklog/ulid/v2 v2.1.1
	github.com/openai/openai-go v1.12.0
	github.com/robfig/cron/v3 v3.0.1
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2
)

require (
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/dlclark/regexp2 v1.11.4 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.28.0 // indirect
	github.com/hollis-labs/go-harness-filters v0.1.0 // indirect
	github.com/hollis-labs/go-queue v0.1.2 // indirect
	github.com/hollis-labs/go-runner v0.5.0 // indirect
	github.com/invopop/jsonschema v0.13.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/sethvargo/go-retry v0.4.0 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/wk8/go-ordered-map/v2 v2.1.8 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.41.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.41.0 // indirect
	go.opentelemetry.io/proto/otlp v1.9.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260720211330-0afa2a65878a // indirect
	google.golang.org/grpc v1.82.1 // indirect
)

// Phase 0 Wave 1 adopted libs (canonical for new code going forward):
require go.uber.org/goleak v1.3.0

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/dgraph-io/ristretto/v2 v2.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/google/flatbuffers v25.2.10+incompatible // indirect
	github.com/hollis-labs/plugin-sdk v0.3.0
	github.com/klauspost/compress v1.19.1 // indirect
	github.com/mattn/go-isatty v0.0.23
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk v1.43.0
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0
	google.golang.org/protobuf v1.36.11 // indirect
	modernc.org/libc v1.74.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace github.com/hollis-labs/go-modelsdev => ../../libs/go-modelsdev

// CW-20260816-0069: local dev against the go-envelopes schema addition
// (report-card.session_link) below, mirroring the go-modelsdev precedent
// above. Points at the sibling checkout in libs/go-envelopes, which has the
// same change committed. Remove once go-envelopes cuts a release that
// includes it and bump the `require` version instead.
replace github.com/hollis-labs/go-envelopes => ../../libs/go-envelopes

// TASKS/agent-host-acp/03: go-agent-wrapper has zero adopters and no
// module-proxy history yet beyond its own v0.1.0 tag pushed to origin --
// v0.2.0 (the tag task 01 cut) only exists in the local sibling checkout,
// so the plain tagged require below doesn't resolve via the proxy. Points
// at the sibling checkout in libs/go-agent-wrapper, mirroring the
// go-modelsdev/go-envelopes precedent above. Remove once go-agent-wrapper
// pushes v0.2.0 (or later) to origin and bump the `require` version
// instead.
replace github.com/hollis-labs/go-agent-wrapper => ../../libs/go-agent-wrapper

// TASKS/agent-host-acp/06: go-harness-filters and go-runtime-events are
// go-agent-wrapper's own transitive deps, now imported directly by Nanite
// too (internal/runtime/agent's runtimeevents.Sink implementation). Per
// this batch's README ("go-harness-filters and go-runtime-events keep the
// older 'drop before tagging' discipline — nothing outside this repo
// depends on their replace staying"), their module-proxy-published v0.1.0
// tags are stale relative to the local sibling checkouts go-agent-wrapper
// was actually built and reviewed against — confirmed directly: building
// without these replaces fails with "undefined: hrepair.Chain" inside
// go-agent-wrapper/filters, a symbol present in the local
// libs/go-harness-filters checkout but not in the proxy-published v0.1.0.
// Mirrors the go-agent-wrapper replace immediately above. Remove once both
// repos cut a release that includes the proxy-published tags catching up.
replace (
	github.com/hollis-labs/go-harness-filters => ../../libs/go-harness-filters
	github.com/hollis-labs/go-runtime-events => ../../libs/go-runtime-events
)

require (
	github.com/hollis-labs/go-agent-wrapper v0.3.0
	github.com/hollis-labs/go-apppaths v0.1.0
	github.com/hollis-labs/go-embed-contracts v0.1.1
	github.com/hollis-labs/go-llm-contracts v0.3.0
	github.com/hollis-labs/go-llm-types v0.3.0
	github.com/hollis-labs/go-messaging v0.2.1
	github.com/hollis-labs/go-runtime-events v0.1.0
	github.com/hollis-labs/go-scheduler v0.1.0
	github.com/hollis-labs/go-sqlite v0.1.0
	github.com/hollis-labs/tesseract v0.7.1-0.20260518032333-bbce958849ac
	github.com/pressly/goose/v3 v3.27.3
)
