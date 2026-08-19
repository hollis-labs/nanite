package broker

// telemetry.go is the breadcrumb-writing seam. Phase 1 declares no new
// surface here — Broker.writeBreadcrumb (in broker.go) already routes
// through Dependencies.Store, which Phase 8 backs with a real
// nanite_recovery_breadcrumbs migration + insert.
//
// Reserved for future telemetry helpers (aggregation, sampling,
// in-memory ring for testing) so they live next to the breadcrumb type
// rather than scattered across the package.
