package workflowhost

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/go-workflow/compile"
)

const preflightDisposition = "operator: keep the service stopped; roll back to the previous binary (database untouched, no new migration), or restore the verified backup"

type workflowPreflightKey struct{}

type workflowStorageFinding struct {
	Severity, Validation, Table, Row, Expected, Actual string
	cause                                              error
}

func (f workflowStorageFinding) String() string {
	return fmt.Sprintf("workflow storage preflight: %s: table=%q row=%q expected=%q actual=%q", f.Validation, f.Table, f.Row, f.Expected, f.Actual)
}

type preflightPlanCheck struct {
	material                          PlanMaterial
	materialErr, identityErr, planErr error
	kind, graphDigest                 string
	qualified                         bool
}

type workflowPreflight struct {
	collect                                    bool
	findings                                   []workflowStorageFinding
	counts                                     map[string]int64
	order                                      []string
	plans                                      map[string]*preflightPlanCheck
	materialChecks, identityChecks, planChecks int
	fatals, warnings                           int
}

func newWorkflowPreflight(collect bool) *workflowPreflight {
	return &workflowPreflight{collect: collect, counts: make(map[string]int64), plans: make(map[string]*preflightPlanCheck)}
}

func preflightState(ctx context.Context) *workflowPreflight {
	if s, ok := ctx.Value(workflowPreflightKey{}).(*workflowPreflight); ok {
		return s
	}
	return newWorkflowPreflight(false)
}

func (s *workflowPreflight) count(name string, n int64) {
	if _, ok := s.counts[name]; !ok {
		s.order = append(s.order, name)
	}
	before := s.counts[name]
	s.counts[name] += n
	if !s.collect && before/1000 != s.counts[name]/1000 {
		slog.Info("workflow storage preflight progress", "validation", name, "rows_scanned", s.counts[name])
	}
}

func (s *workflowPreflight) finding(severity, validation, table, row, expected string, actual any) error {
	f := workflowStorageFinding{Severity: severity, Validation: validation, Table: table, Row: row, Expected: expected, Actual: fmt.Sprint(actual)}
	if err, ok := actual.(error); ok {
		f.cause = err
	}
	if s.collect {
		s.findings = append(s.findings, f)
	}
	if severity == "warning" {
		s.warnings++
		if !s.collect {
			slog.Warn(f.String())
		}
		return nil
	}
	s.fatals++
	if s.collect {
		return nil
	}
	if f.cause != nil {
		return fmt.Errorf("%s; %s: %w", f.String(), preflightDisposition, f.cause)
	}
	return fmt.Errorf("%s; %s", f.String(), preflightDisposition)
}

func (s *workflowPreflight) result() error {
	if s.fatals == 0 {
		return nil
	}
	for _, finding := range s.findings {
		if finding.Severity == "fatal" {
			return fmt.Errorf("%s; fatal_findings=%d; %s", finding.String(), s.fatals, preflightDisposition)
		}
	}
	return fmt.Errorf("workflow storage preflight: fatal_findings=%d; %s", s.fatals, preflightDisposition)
}

func (s *workflowPreflight) material(ctx context.Context, tx workflowSQL, digest string) *preflightPlanCheck {
	if cached, ok := s.plans[digest]; ok {
		return cached
	}
	check := &preflightPlanCheck{}
	s.plans[digest] = check
	s.count("material-validations", 0)
	check.material, check.materialErr = readPlanMaterial(ctx, tx, digest)
	if check.materialErr == nil {
		s.materialChecks++
		s.count("material-validations", 1)
		check.materialErr = validatePlanMaterial(check.material)
		if check.materialErr == nil {
			check.graphDigest, check.materialErr = compile.GraphDigest(check.material.Plan.Graph)
		}
	}
	return check
}

func (s *workflowPreflight) qualify(ctx context.Context, check *preflightPlanCheck) {
	if check.qualified || check.materialErr != nil {
		return
	}
	check.qualified = true
	s.count("installed-identity-validations", 0)
	s.count("compiled-plan-validations", 0)
	check.kind = EngineKindGoWorkflow
	registry, err := newFrozenRegistry(storagePreflightExecutor{})
	_, pilotDigest, pilotErr := pilotHostContract()
	if pilotErr == nil && check.material.HostContractDigest == pilotDigest {
		check.kind = EngineKindPilotHadron
		registry, err = newPilotRegistry(storagePreflightExecutor{})
	}
	if err != nil {
		check.identityErr = err
		return
	}
	s.identityChecks++
	s.count("installed-identity-validations", 1)
	verifiers, err := verifyInstalledExecutionIdentity(check.material, registry)
	check.identityErr = err
	if err != nil {
		return
	}
	s.planChecks++
	s.count("compiled-plan-validations", 1)
	findings := compile.ValidatePlan(ctx, &check.material.Plan, compile.ValidationOptions{StepKinds: registry, Verifiers: verifiers})
	if hasDiagnosticErrors(findings) {
		check.planErr = diagnosticsError("unsupported frozen execution plan", findings)
	}
}
