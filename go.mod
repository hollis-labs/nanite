module github.com/hollis-labs/conduit

go 1.26.1

require (
	github.com/google/uuid v1.6.0
	github.com/hollis-labs/fragments-engine/connectors/gmail v0.0.0
	github.com/hollis-labs/fragments-engine/connectors/webhook v0.0.0
	github.com/hollis-labs/fragments-engine/plugin v0.0.0
	github.com/hollis-labs/nexus v0.0.0
	github.com/hollis-labs/otel v0.0.0
	github.com/hollis-labs/tool-broker v0.0.0
	github.com/joho/godotenv v1.5.1
	github.com/lib/pq v1.12.0
	go.opentelemetry.io/otel v1.41.0
	go.opentelemetry.io/otel/trace v1.41.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.46.1
)

replace github.com/hollis-labs/otel => ../libs/otel

replace github.com/hollis-labs/tool-broker => ../libs/toolbroker

replace github.com/hollis-labs/fragments-engine/plugin => ../libs/plugin

replace github.com/hollis-labs/fragments-engine/connectors/gmail => ../libs/connectors/gmail

replace github.com/hollis-labs/fragments-engine/connectors/webhook => ../libs/connectors/webhook

replace github.com/hollis-labs/nexus => ../../nexus

require (
	cloud.google.com/go/auth v0.18.2 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.14 // indirect
	github.com/googleapis/gax-go/v2 v2.18.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.28.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.61.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.41.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.41.0 // indirect
	go.opentelemetry.io/otel/metric v1.41.0 // indirect
	go.opentelemetry.io/otel/sdk v1.41.0 // indirect
	go.opentelemetry.io/proto/otlp v1.9.0 // indirect
	golang.org/x/crypto v0.49.0 // indirect
	golang.org/x/exp v0.0.0-20251023183803-a4bb9ffd2546 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	google.golang.org/api v0.272.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260217215200-42d3e9bedb6d // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260311181403-84a4fc48630c // indirect
	google.golang.org/grpc v1.79.2 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	modernc.org/libc v1.67.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
