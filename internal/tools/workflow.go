package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/workflow"
)

// defaultWorkflowConcurrency caps how many agent() calls run at once inside one
// workflow, so a parallel() over a large list can't fan out an unbounded number
// of concurrent LLM turns.
const defaultWorkflowConcurrency = 8

// workflowForeground, when set, makes the workflow tool run scripts to
// completion inside the tool call instead of detaching a background run. The
// headless one-shot enables it: that mode exits when the turn ends (killing
// still-running background work), so a detached run could never deliver its
// result — the completion notification the background texts promise would
// never arrive. Interactive transports (TUI, web, IM) leave it off.
var workflowForeground atomic.Bool

// SetWorkflowForeground toggles foreground (blocking) workflow execution for
// this process. Set it before the tool definitions are built — the workflow
// tool's description tells the model which mode it is in.
func SetWorkflowForeground(v bool) { workflowForeground.Store(v) }

// workflowJournalDir overrides the workflow runtime's journal directory
// (~/.octo/workflow-journals by default, resolved by internal/workflow).
// Empty (the zero value) leaves the runtime default in place — every real
// entry point (CLI, server, IM) never sets this. Tests point it at a temp dir
// so running the suite doesn't write into a developer's real journal
// directory.
var workflowJournalDir atomic.Value // string

// SetWorkflowJournalDir overrides the workflow runtime's journal directory for
// this process. Pass "" to restore the default.
func SetWorkflowJournalDir(dir string) { workflowJournalDir.Store(dir) }

// ActiveWorkflowJournalDir returns the dir set by SetWorkflowJournalDir, or ""
// (the runtime default) if never set.
func ActiveWorkflowJournalDir() string {
	v, _ := workflowJournalDir.Load().(string)
	return v
}

// WorkflowTool runs a Ruby (mruby) orchestration script in an embedded wasm
// interpreter. The script drives sub-agents through the agent() / parallel() /
// pipeline() primitives; each agent() call delegates to the same Spawner that
// backs sub_agent. The tool is advertised only when a Spawner is registered.
//
// Like sub_agent, it refuses to run inside a sub-agent — workflow agents are
// themselves marked as sub-agents, so a child can't recursively launch another
// workflow.
type WorkflowTool struct{}

func (WorkflowTool) Definition() agent.ToolDefinition {
	// The execution contract differs by transport: interactive transports run
	// workflows detached (with a completion notification), the headless
	// one-shot runs them inline. Tell the model which one it gets.
	mode := "Runs in the BACKGROUND: returns a run id at once and the result arrives as a notification; do NOT poll workflow_status."
	if workflowForeground.Load() {
		mode = "Runs in the FOREGROUND: blocks until the script finishes and returns the result directly (no run id, nothing to poll)."
	}
	return agent.ToolDefinition{
		Name:        "workflow",
		Description: "Run a Ruby script that orchestrates many sub-agent calls with explicit control flow (fan-out, pipelines, loops, conditionals), inline via `script` or a saved one by `name`.\n\n" + mode + "\n\nThe script runs in sandboxed, IO-free mruby: Array/Hash/String/Integer/Regexp and JSON.parse/JSON.generate only. Regexp is RE2: no backreferences or lookaround in patterns (\\1/\\k<name> work in gsub/sub replacements), use /m (not inline (?m)) for dot-all, no /x, use \\z not \\Z. There is NO File, Dir, Time, Process or backticks (referencing them errors). Filesystem, shell/git/gh and the date are reachable only through agent(); have an agent write any report or state file. For structured output use agent(prompt, schema: ...) and JSON.parse it rather than regex-scraping prose.\nPrimitives:\n- `agent(prompt, opts={}) -> String`: one sub-agent to completion (concurrent inside parallel/pipeline). opts: `model:` (\"lite\" for mechanical stages), `tools:` (Array), `read_only: true`, `schema:` (JSON Schema as a JSON string; reply is matching JSON), `isolation: \"worktree\"` (fresh git worktree; changes left on a branch named in the reply).\n- `parallel(items) { |it| ... } -> Array` (input order); `pipeline(items, stage1, stage2, ...) -> Array` (lambdas; items flow independently).\n- `log(msg)`, `phase(title)` (progress grouping), `budget_remaining -> Integer`, `args` (the `args` parameter as native Ruby, nil when none).\nThe final expression is the result. Example:\n```ruby\nfindings = parallel(%w[auth db cache]) { |area| agent(\"Audit the #{area} module for bugs\") }\n\"Reviewed #{findings.size} modules:\\n\" + findings.join(\"\\n---\\n\")\n```",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"script": map[string]any{
					"type":        "string",
					"description": "The Ruby script; its last expression is the result. Give exactly one of script or name.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": savedWorkflowsParamDesc(),
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Short label, 3-7 words, shown in the UI.",
				},
				"args": map[string]any{
					"type":        "object",
					"description": "Input for the script, read via the `args` primitive.",
				},
				"resume_from": map[string]any{
					"type":        "string",
					"description": "Run id (wf-YYYYMMDD-HHMMSS-xxxxxxxx, from a previous \"[workflow run: ...]\") to resume; finished agent() calls replay instantly. The script must be identical.",
				},
			},
		},
	}
}

// instrumentWorkflowAgent wires one agent()/skill() call into the owning
// run's structured event stream: it emits "agent_started", stamps a sub-agent
// event sink into ctx that translates the child's tool/text activity (see
// Spawner.runChild) onto the corresponding agent_* workflow kinds, and returns
// a done func that emits "agent_done" with the capped reply or the error.
// Outside a managed run (no emitter in ctx) it is a no-op. seq is the run's
// shared agent counter; safe for concurrent agent() calls under parallel().
func instrumentWorkflowAgent(ctx context.Context, seq *int32, label string) (context.Context, func(reply string, err error)) {
	emit := workflowEmitFrom(ctx)
	if emit == nil {
		return ctx, func(string, error) {}
	}
	agentID := fmt.Sprintf("a%d", atomic.AddInt32(seq, 1))
	emit(WorkflowEvent{Kind: "agent_started", AgentID: agentID, AgentLabel: label})
	ctx = WithSubAgentEventSink(ctx, func(se SubAgentEvent) {
		switch se.Kind {
		case "tool":
			emit(WorkflowEvent{Kind: "agent_tool", AgentID: agentID, ToolID: se.ToolID, ToolName: se.ToolName, ToolInput: se.ToolInput})
		case "tool_done":
			emit(WorkflowEvent{Kind: "agent_tool_done", AgentID: agentID, ToolID: se.ToolID, ToolName: se.ToolName, ToolOutput: se.ToolOutput})
		case "tool_error":
			emit(WorkflowEvent{Kind: "agent_tool_error", AgentID: agentID, ToolID: se.ToolID, ToolName: se.ToolName, ToolOutput: se.ToolOutput})
		case "text":
			emit(WorkflowEvent{Kind: "agent_text", AgentID: agentID, Text: se.Text})
		}
	})
	return ctx, func(reply string, err error) {
		ev := WorkflowEvent{Kind: "agent_done", AgentID: agentID, AgentLabel: label}
		if err != nil {
			ev.Error = err.Error()
		} else {
			ev.Reply = ClipForEvent(reply, SubAgentEventResultCap)
		}
		emit(ev)
	}
}

// savedWorkflowsParamDesc builds the `name` parameter description, listing the
// saved workflows currently in the registry (~/.octo/workflows) so the model
// knows what it can run by name.
func savedWorkflowsParamDesc() string {
	var b strings.Builder
	b.WriteString("Run a saved workflow by name (from ~/.octo/workflows). " +
		"Provide exactly one of script or name; args are passed in either way.")
	saved := listWorkflows()
	if len(saved) == 0 {
		b.WriteString(" (No saved workflows found yet — author one with workflow_save.)")
		return b.String()
	}
	b.WriteString(" Available:")
	for _, w := range saved {
		line := "\n- " + w.name
		if w.description != "" {
			line += " — " + w.description
		}
		if req := requiredParamNames(w.params); len(req) > 0 {
			line += " (requires: " + strings.Join(req, ", ") + ")"
		}
		b.WriteString(line)
	}
	return b.String()
}

// requiredParamNames returns the names of params marked required, in
// declaration order.
func requiredParamNames(params []workflowParam) []string {
	var out []string
	for _, p := range params {
		if p.required {
			out = append(out, p.name)
		}
	}
	return out
}

// ensureRequiredWorkflowParams checks a saved workflow's declared `# @param
// name required ...` inputs against the tool's `args` input. Any that are
// missing produce an error listing them — the model then decides whether to
// re-invoke with `args` filled in (it already knows the values) or surface an
// `ask_user_question` to the caller (it genuinely doesn't know). The old
// behaviour auto-prompted via the ctx Asker, which stole that decision from the
// model and caused repeat prompts when the model had already asked the user.
func ensureRequiredWorkflowParams(workflowName string, params []workflowParam, args map[string]any) error {
	var missing []string
	for _, p := range params {
		if !p.required {
			continue
		}
		if v, ok := args[p.name]; ok && v != nil && v != "" {
			continue
		}
		missing = append(missing, p.name)
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("workflow %q is missing required arg(s): %s — pass them in `args`",
		workflowName, strings.Join(missing, ", "))
}

// Execute runs the workflow: detached with a run handle in background mode,
// or to completion with the result returned inline in foreground mode.
func (WorkflowTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	if IsSubAgent(ctx) {
		return agent.ToolResult{}, fmt.Errorf("workflow: a sub-agent cannot run a workflow")
	}

	script := strings.TrimSpace(stringArg(input, "script"))
	name := strings.TrimSpace(stringArg(input, "name"))
	description := strings.TrimSpace(stringArg(input, "description"))
	switch {
	case script != "" && name != "":
		return agent.ToolResult{}, fmt.Errorf("workflow: provide exactly one of script or name, not both")
	case script == "" && name == "":
		return agent.ToolResult{}, fmt.Errorf("workflow: provide a script, or a name of a saved workflow")
	case name != "":
		w, ok := lookupWorkflow(name)
		if !ok {
			return agent.ToolResult{}, fmt.Errorf("workflow: no saved workflow named %q (looked in ~/.octo/workflows)", name)
		}
		script = w.script
		if description == "" {
			description = w.description
		}
		if len(w.params) > 0 {
			argsMap, _ := input["args"].(map[string]any)
			if err := ensureRequiredWorkflowParams(name, w.params, argsMap); err != nil {
				return agent.ToolResult{}, err
			}
		}
	}

	// Prefer the ctx-scoped SubAgentManager (server/IM per-turn/per-session) so a
	// workflow spawned by one session doesn't accidentally use another session's
	// global Spawner under concurrency. Fall back to the process-global Spawner
	// for CLI/TUI paths that never stamp a ctx manager.
	var spawner Spawner
	if mgr := resolveSubAgentManager(ctx, nil); mgr != nil {
		spawner = mgr.Spawner()
	}
	if spawner == nil {
		spawner = ActiveSpawner()
	}
	if spawner == nil {
		return agent.ToolResult{}, fmt.Errorf("workflow: sub-agent dispatch is not configured for this session")
	}

	// agent() delegates to the same Spawner that backs sub_agent. The Spawner
	// marks children as sub-agents, so a workflow agent can't recurse. Each
	// call is instrumented onto the run's structured event stream (agent_*
	// kinds) so panels can show its tool trail and reply, not just log lines.
	var agentSeq int32
	af := func(c context.Context, prompt string, opts workflow.AgentOptions) workflow.AgentResult {
		c, done := instrumentWorkflowAgent(c, &agentSeq, firstLine(prompt))
		res, err := spawner.Spawn(c, SpawnRequest{
			Description: firstLine(prompt),
			Prompt:      prompt,
			Model:       opts.Model,
			Tools:       opts.Tools,
			ReadOnly:    opts.ReadOnly,
			Schema:      opts.Schema,
			Isolation:   opts.Isolation,
		})
		if err != nil {
			done("", err)
			return workflow.AgentResult{Err: err}
		}
		done(res.Reply, nil)
		return workflow.AgentResult{
			Reply:        res.Reply,
			InputTokens:  res.InputTokens,
			OutputTokens: res.OutputTokens,
		}
	}

	// skill() runs a SKILL.md skill as a sub-agent via the same spawner;
	// recording() replays a browser recording deterministically. Both arrive
	// through this one channel — see dispatchWorkflowSkill for the routing.
	sf := func(c context.Context, name, paramsJSON, schema string) workflow.AgentResult {
		c, done := instrumentWorkflowAgent(c, &agentSeq, "skill: "+name)
		r := dispatchWorkflowSkill(c, spawner, name, paramsJSON, schema)
		done(r.Reply, r.Err)
		return r
	}

	argsJSON, err := encodeWorkflowArgs(input["args"])
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("workflow: args must be a JSON value: %w", err)
	}

	mgr := resolveWorkflowManager(ctx)
	req := WorkflowRunRequest{
		Description:   description,
		Script:        script,
		Args:          argsJSON,
		Agent:         af,
		Skill:         sf,
		MaxConcurrent: defaultWorkflowConcurrency,
		ResumeFrom:    stringArg(input, "resume_from"),
		Foreground:    workflowForeground.Load(),
		// Carried into Start's detached context so the script's own
		// agent()/skill() calls (and anything nested they do, like
		// workflow_save) still resolve against this turn's directory instead
		// of falling back to the server's own launch directory once the run
		// is no longer tied to this request's ctx.
		WorkingDir:        WorkingDirOrCWD(ctx),
		UpstreamSessionID: agent.UpstreamSessionIDFrom(ctx),
	}

	runID, err := mgr.Start(req)
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("workflow: %w", err)
	}

	// Foreground (one-shot) mode: run to completion inside this call and hand
	// the result back directly. Going through the manager keeps the run visible
	// to progress hooks and workflow_status, and enforces the concurrency cap.
	if req.Foreground {
		snap, werr := mgr.Wait(ctx, runID)
		if werr != nil {
			return agent.ToolResult{}, fmt.Errorf("workflow: run interrupted: %w", werr)
		}
		return agent.ToolResult{Text: formatRunDetail(snap), UI: workflowResultUI(runID)}, nil
	}

	return agent.ToolResult{Text: fmt.Sprintf(
		"Workflow started in the background as %s.\n"+
			"<system-reminder>This run executes in the background. DO NOT poll workflow_status "+
			"while it runs — the system will automatically notify you when it finishes, carrying "+
			"the result. While it runs, you may continue with other independent tasks. If you have "+
			"no other task to do, report the launch to the user and stop — do not spin in a "+
			"polling loop. (workflow_status(%q) exists for on-demand progress checks, e.g. when "+
			"the user asks.)</system-reminder>", runID, runID),
		UI: workflowResultUI(runID)}, nil
}

// workflowResultUI is the structured payload on a workflow tool result that
// lets the web transcript's tool card claim the run's event trail (see
// dev-docs/agent-run-panel-design.md). It persists with the session on the
// tool_result block and never reaches the model.
func workflowResultUI(runID string) map[string]any {
	return map[string]any{"run_id": runID}
}

// encodeWorkflowArgs serializes the tool's `args` input to the JSON string the
// workflow runtime serves to the script's args primitive. A nil/absent value
// yields "" (the script's args returns nil).
func encodeWorkflowArgs(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// WorkflowStatusTool reports on background workflow runs: a list with no
// argument, or one run's full status + result when given a run id.
type WorkflowStatusTool struct{}

func (WorkflowStatusTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "workflow_status",
		Description: "Check workflow runs: no run_id lists this session's runs (running/done/error); a run_id gives its result or error (with how to fix/resume) and log. For on-demand checks or collecting a result after the notification; never poll (repeated still-running reads trigger a STOP reminder).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"run_id": map[string]any{
					"type":        "string",
					"description": "A run id from the workflow tool (e.g. \"wf_1\"). Omit to list all runs.",
				},
			},
		},
	}
}

// workflowPollStopText is the hard stop appended once a no-progress polling
// streak crosses workflowPollStopThreshold. The agent loop exempts
// workflow_status from its duplicate-tool-call detector (it is a legitimate
// observation tool), so this guard is what breaks a polling loop — mirroring
// terminal_output's empty-snapshot guard.
const workflowPollStopText = "\n\n[STOP: repeated workflow_status polling detected. " +
	"Do not poll again. The system will push a notification with the result " +
	"when a run finishes.]"

func (WorkflowStatusTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	mgr := resolveWorkflowManager(ctx)
	runID := strings.TrimSpace(stringArg(input, "run_id"))
	if runID == "" {
		runs := mgr.List()
		if len(runs) == 0 {
			return agent.ToolResult{Text: "No background workflows have been started in this session."}, nil
		}
		lines := make([]string, 0, len(runs))
		var latest time.Time
		anyRunning := false
		for _, r := range runs {
			lines = append(lines, statusLine(r))
			if r.Status == "running" {
				anyRunning = true
				if r.LastActivity.After(latest) {
					latest = r.LastActivity
				}
			}
		}
		text := strings.Join(lines, "\n")
		// The list form observes running work too; give it the same anti-poll
		// escalation, keyed under "" off the freshest activity across running runs.
		if mgr.RecordStatusRead("", anyRunning, latest) >= workflowPollStopThreshold {
			text += workflowPollStopText
		}
		return agent.ToolResult{Text: text}, nil
	}
	snap, ok := mgr.Read(runID)
	if !ok {
		return agent.ToolResult{}, fmt.Errorf("workflow_status: no run named %q in this session", runID)
	}
	text := formatRunDetail(snap)
	if mgr.RecordStatusRead(runID, snap.Status == "running", snap.LastActivity) >= workflowPollStopThreshold {
		text += workflowPollStopText
	}
	return agent.ToolResult{Text: text}, nil
}

// WorkflowKillTool cancels a running background workflow by id — for a run that
// has stalled (workflow_status shows a large, growing "last activity" gap) or
// is no longer wanted.
type WorkflowKillTool struct{}

func (WorkflowKillTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "workflow_kill",
		Description: "Cancel a running workflow by run id (e.g. \"wf_1\") when it is stuck (growing 'last activity' gap) or no longer wanted; its in-flight sub-agents are cancelled too. Finished runs are left as-is.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"run_id": map[string]any{
					"type":        "string",
					"description": "The run id to cancel (from the workflow tool / workflow_status).",
				},
			},
			"required": []string{"run_id"},
		},
	}
}

func (WorkflowKillTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	runID := strings.TrimSpace(stringArg(input, "run_id"))
	if runID == "" {
		return agent.ToolResult{}, fmt.Errorf("workflow_kill: run_id is required")
	}
	mgr := resolveWorkflowManager(ctx)
	found, wasRunning := mgr.Kill(runID)
	switch {
	case !found:
		return agent.ToolResult{}, fmt.Errorf("workflow_kill: no run named %q in this session", runID)
	case !wasRunning:
		return agent.ToolResult{Text: fmt.Sprintf("Workflow %s had already finished — nothing to cancel.", runID)}, nil
	default:
		return agent.ToolResult{Text: fmt.Sprintf("Cancelled workflow %s. It will report as killed shortly; "+
			"workflow_status(%q) confirms.", runID, runID)}, nil
	}
}
