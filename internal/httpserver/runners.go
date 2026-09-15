package httpserver

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/experiment"
	"github.com/mhingston/skillet/internal/runner"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const externalImprovementRunnersEnv = "SKILLET_EXTERNAL_IMPROVEMENT_RUNNERS"

var externalRunnerStores sync.Map // map[*Server]*runner.Store

type registerExternalImprovementRunnerInput struct {
	RunnerID             string       `json:"runner_id" jsonschema:"Stable external runner identity"`
	Version              string       `json:"version" jsonschema:"Immutable runner implementation/configuration version"`
	Capabilities         []string     `json:"capabilities" jsonschema:"Bounded declared runner capabilities"`
	AcceptedSpecVersions []string     `json:"accepted_spec_versions" jsonschema:"Exact improvement experiment spec versions accepted by the runner"`
	Scope                runner.Scope `json:"scope" jsonschema:"Least-privilege exact capability and budget authorization scope"`
	PublicKey            string       `json:"public_key" jsonschema:"Base64 Ed25519 public key used to authenticate status/result evidence; private runner credentials remain external"`
}

type externalRunnerRegistrationInput struct {
	RunnerID string `json:"runner_id" jsonschema:"Stable external runner identity"`
	Version  string `json:"version" jsonschema:"Exact registered runner version"`
}

type dispatchImprovementExperimentInput struct {
	ExperimentID         string   `json:"experiment_id" jsonschema:"Exact issued M4.1 improvement experiment identifier"`
	RunnerID             string   `json:"runner_id" jsonschema:"Registered external runner identity"`
	RunnerVersion        string   `json:"runner_version" jsonschema:"Exact registered runner version"`
	RequiredCapabilities []string `json:"required_capabilities" jsonschema:"Capabilities this bounded experiment requires from the runner"`
	CorrelationID        string   `json:"correlation_id,omitempty" jsonschema:"Optional caller correlation identifier"`
}

type externalRunnerRunInput struct {
	RunID string `json:"run_id" jsonschema:"Skillet-owned external runner execution identifier"`
}

type recordExternalRunnerStatusInput struct {
	Event runner.StatusEnvelope `json:"event" jsonschema:"Runner-signed, monotonically sequenced non-terminal status evidence"`
}

type submitExternalRunnerResultInput struct {
	Result runner.ResultEnvelope `json:"result" jsonschema:"Runner-signed terminal result evidence bound to exact dispatch/spec digests"`
}

type externalRunnerResultOutput struct {
	Run        runner.Run            `json:"run"`
	Experiment experiment.Experiment `json:"experiment"`
}

func addExternalRunnerTools(server *mcp.Server, app *Server) {
	if server == nil || app == nil || app.catalogue == nil || !externalImprovementRunnersEnabled() {
		return
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "register_external_improvement_runner",
		Description: "Register one immutable, organisation-scoped external runner identity/version, Ed25519 public key, capabilities, accepted experiment spec versions, and least-privilege capability/budget scope. No private credentials are stored.",
	}, app.registerExternalImprovementRunnerTool)
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_external_improvement_runner",
		Description: "Read one exact immutable external runner registration.",
	}, app.getExternalImprovementRunnerTool)
	mcp.AddTool(server, &mcp.Tool{
		Name: "dispatch_improvement_experiment",
		Description: "Create a deterministic, idempotent external-runner dispatch for one issued experiment. This returns data only: Skillet never starts a worker, invokes a shell, or calls the runner.",
	}, app.dispatchImprovementExperimentTool)
	mcp.AddTool(server, &mcp.Tool{
		Name: "record_external_runner_status",
		Description: "Verify and record runner-signed accepted/running evidence with exact run/spec/handoff/dispatch binding and monotonic replay-safe sequencing.",
	}, app.recordExternalRunnerStatusTool)
	mcp.AddTool(server, &mcp.Tool{
		Name: "submit_external_runner_result",
		Description: "Verify runner-signed terminal result evidence, preserve resource/cost/runtime/log references, and submit only raw protected measurements to the immutable M4.1 threshold evaluator.",
	}, app.submitExternalRunnerResultTool)
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_external_runner_run",
		Description: "Poll one external-runner control-plane record, including exact dispatch identity, latest state, signed terminal evidence, and Skillet-derived budget assessment.",
	}, app.getExternalRunnerRunTool)
}

func externalImprovementRunnersEnabled() bool {
	return improvementExperimentsEnabled() && strings.EqualFold(strings.TrimSpace(os.Getenv(externalImprovementRunnersEnv)), "true")
}

func (s *Server) registerExternalImprovementRunnerTool(ctx context.Context, _ *mcp.CallToolRequest, input registerExternalImprovementRunnerInput) (*mcp.CallToolResult, runner.Registration, error) {
	organizationID, actorID := s.proposalActor(ctx)
	resource := authz.Resource{OrganizationID: organizationID, ID: strings.TrimSpace(input.RunnerID)}
	if err := s.authorize(ctx, authz.ActionRunnerRegister, resource); err != nil {
		return nil, runner.Registration{}, err
	}
	store, err := s.externalRunnerStore(ctx)
	if err != nil {
		return nil, runner.Registration{}, err
	}
	item, err := store.Register(ctx, runner.RegisterInput{
		OrganizationID: organizationID,
		ActorID: actorID,
		RunnerID: input.RunnerID,
		Version: input.Version,
		Capabilities: input.Capabilities,
		AcceptedSpecVersions: input.AcceptedSpecVersions,
		Scope: input.Scope,
		PublicKey: input.PublicKey,
	})
	if err != nil {
		return nil, runner.Registration{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "external_improvement_runner_registered", map[string]any{
		"actor_type": "human_or_control_plane", "actor_id": actorID, "runner_id": item.RunnerID,
		"runner_version": item.Version, "registration_sha256": item.RegistrationSHA256,
	}); err != nil {
		return nil, runner.Registration{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Registered immutable external runner metadata and public verification key. No private credentials or execution authority were stored."}}}, item, nil
}

func (s *Server) getExternalImprovementRunnerTool(ctx context.Context, _ *mcp.CallToolRequest, input externalRunnerRegistrationInput) (*mcp.CallToolResult, runner.Registration, error) {
	organizationID, _ := s.proposalActor(ctx)
	resource := authz.Resource{OrganizationID: organizationID, ID: strings.TrimSpace(input.RunnerID)}
	if err := s.authorize(ctx, authz.ActionRunnerRead, resource); err != nil {
		return nil, runner.Registration{}, err
	}
	store, err := s.externalRunnerStore(ctx)
	if err != nil {
		return nil, runner.Registration{}, err
	}
	item, err := store.GetRegistration(ctx, organizationID, input.RunnerID, input.Version)
	if err != nil {
		return nil, runner.Registration{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Returned immutable external runner registration evidence."}}}, item, nil
}

func (s *Server) dispatchImprovementExperimentTool(ctx context.Context, _ *mcp.CallToolRequest, input dispatchImprovementExperimentInput) (*mcp.CallToolResult, runner.Run, error) {
	organizationID, actorID := s.proposalActor(ctx)
	resource, err := s.experimentResource(ctx, organizationID, strings.TrimSpace(input.ExperimentID))
	if err != nil {
		return nil, runner.Run{}, err
	}
	if err := s.authorize(ctx, authz.ActionRunnerDispatch, resource); err != nil {
		return nil, runner.Run{}, err
	}
	if err := s.authorize(ctx, authz.ActionExperimentRead, resource); err != nil {
		return nil, runner.Run{}, err
	}
	store, err := s.externalRunnerStore(ctx)
	if err != nil {
		return nil, runner.Run{}, err
	}
	item, err := store.Issue(ctx, runner.IssueInput{
		OrganizationID: organizationID,
		ActorID: actorID,
		CorrelationID: strings.TrimSpace(input.CorrelationID),
		ExperimentID: strings.TrimSpace(input.ExperimentID),
		RunnerID: strings.TrimSpace(input.RunnerID),
		RunnerVersion: strings.TrimSpace(input.RunnerVersion),
		RequiredCapabilities: input.RequiredCapabilities,
	})
	if err != nil {
		return nil, runner.Run{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "external_improvement_runner_dispatched", map[string]any{
		"actor_type": "human_or_control_plane", "actor_id": actorID, "skill_id": resource.ID,
		"experiment_id": item.ExperimentID, "run_id": item.ID, "runner_id": item.RunnerID,
		"runner_version": item.RunnerVersion, "dispatch_sha256": item.DispatchSHA256,
	}); err != nil {
		return nil, runner.Run{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Issued deterministic external dispatch data. Skillet did not invoke the runner or execute any workload."}}}, item, nil
}

func (s *Server) recordExternalRunnerStatusTool(ctx context.Context, _ *mcp.CallToolRequest, input recordExternalRunnerStatusInput) (*mcp.CallToolResult, runner.Run, error) {
	organizationID, _ := s.proposalActor(ctx)
	resource, err := s.externalRunnerRunResource(ctx, organizationID, input.Event.RunID)
	if err != nil {
		return nil, runner.Run{}, err
	}
	if err := s.authorize(ctx, authz.ActionRunnerRecordStatus, resource); err != nil {
		return nil, runner.Run{}, err
	}
	store, err := s.externalRunnerStore(ctx)
	if err != nil {
		return nil, runner.Run{}, err
	}
	item, err := store.RecordStatus(ctx, organizationID, input.Event)
	if err != nil {
		return nil, runner.Run{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Verified and recorded signed external runner status evidence."}}}, item, nil
}

func (s *Server) submitExternalRunnerResultTool(ctx context.Context, _ *mcp.CallToolRequest, input submitExternalRunnerResultInput) (*mcp.CallToolResult, externalRunnerResultOutput, error) {
	organizationID, actorID := s.proposalActor(ctx)
	resource, err := s.externalRunnerRunResource(ctx, organizationID, input.Result.RunID)
	if err != nil {
		return nil, externalRunnerResultOutput{}, err
	}
	if err := s.authorize(ctx, authz.ActionRunnerSubmitResult, resource); err != nil {
		return nil, externalRunnerResultOutput{}, err
	}
	if err := s.authorize(ctx, authz.ActionExperimentSubmitResult, resource); err != nil {
		return nil, externalRunnerResultOutput{}, err
	}
	store, err := s.externalRunnerStore(ctx)
	if err != nil {
		return nil, externalRunnerResultOutput{}, err
	}
	runItem, exp, err := store.SubmitResult(ctx, organizationID, actorID, input.Result)
	if err != nil {
		return nil, externalRunnerResultOutput{}, err
	}
	if err := s.recordAudit(ctx, organizationID, "external_improvement_runner_result_recorded", map[string]any{
		"actor_type": "external_runner", "runner_id": runItem.RunnerID, "runner_version": runItem.RunnerVersion,
		"skill_id": exp.CapabilityID, "revision_id": exp.Base.RevisionID, "experiment_id": exp.ID,
		"run_id": runItem.ID, "dispatch_sha256": runItem.DispatchSHA256, "result_sha256": runItem.ResultSHA256,
		"runner_status": runItem.State, "experiment_status": exp.Status,
	}); err != nil {
		return nil, externalRunnerResultOutput{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Authenticated exact external-runner evidence and applied the pre-existing immutable experiment thresholds. Runner text/artifacts remain untrusted evidence; canonical source, ranking, governance, and policy were not changed."}}}, externalRunnerResultOutput{Run: runItem, Experiment: exp}, nil
}

func (s *Server) getExternalRunnerRunTool(ctx context.Context, _ *mcp.CallToolRequest, input externalRunnerRunInput) (*mcp.CallToolResult, runner.Run, error) {
	organizationID, _ := s.proposalActor(ctx)
	resource, err := s.externalRunnerRunResource(ctx, organizationID, strings.TrimSpace(input.RunID))
	if err != nil {
		return nil, runner.Run{}, err
	}
	if err := s.authorize(ctx, authz.ActionRunnerRead, resource); err != nil {
		return nil, runner.Run{}, err
	}
	store, err := s.externalRunnerStore(ctx)
	if err != nil {
		return nil, runner.Run{}, err
	}
	item, err := store.GetRun(ctx, organizationID, strings.TrimSpace(input.RunID))
	if err != nil {
		return nil, runner.Run{}, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Returned external runner control-plane state %q for exact run %s.", item.State, item.ID)}}}, item, nil
}

func (s *Server) externalRunnerStore(ctx context.Context) (*runner.Store, error) {
	if existing, ok := externalRunnerStores.Load(s); ok {
		store, _ := existing.(*runner.Store)
		if store != nil {
			return store, nil
		}
	}
	experiments, err := s.experimentStore(ctx)
	if err != nil {
		return nil, err
	}
	store, err := runner.New(ctx, s.catalogue, experiments)
	if err != nil {
		return nil, err
	}
	actual, _ := externalRunnerStores.LoadOrStore(s, store)
	resolved, _ := actual.(*runner.Store)
	if resolved == nil {
		return nil, fmt.Errorf("external runner persistence is unavailable")
	}
	return resolved, nil
}

func (s *Server) externalRunnerRunResource(ctx context.Context, organizationID, runID string) (authz.Resource, error) {
	store, err := s.externalRunnerStore(ctx)
	if err != nil {
		return authz.Resource{}, err
	}
	item, err := store.GetRun(ctx, organizationID, strings.TrimSpace(runID))
	if err != nil {
		return authz.Resource{}, err
	}
	return s.experimentResource(ctx, organizationID, item.ExperimentID)
}
