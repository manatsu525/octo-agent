// Package tools provides built-in tool implementations for the octo agentic
// loop. Each tool implements agent.ToolExecutor and exposes a Definition()
// method that returns the agent.ToolDefinition the LLM sees.
package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/panics"
)

// TerminalTimeout is the DEFAULT wait for a synchronous command when the call
// doesn't pass an explicit `timeout`. On expiry the command is killed and an
// error is returned — it is NOT auto-promoted to the background (a caller that
// wants a long-running or detached process asks for one explicitly). A var so
// tests can shorten it.
var TerminalTimeout = 120 * time.Second

// MaxTerminalTimeout caps the explicit `timeout` a call may request, so a model
// can't block the turn for an unbounded stretch waiting on one command. A
// request above this is rejected with a parameter error that points at
// run_in_background instead of being silently clamped.
const MaxTerminalTimeout = 10 * time.Minute

// AsyncModeNotice is the model-facing instruction appended to an async
// background-launch tool result. Wrapped in <system-reminder> so
// StripRemindersForDisplay strips it from UI cards (TUI and web).
const AsyncModeNotice = "<system-reminder>This is an ASYNC background process (a one-shot task). DO NOT use terminal_output or terminal_input on it. The system will automatically notify you when it finishes, carrying its final output. While it runs, you may continue with other independent tasks. If you have no other task to do, report the launch to the user and stop — do not spin in a polling loop.</system-reminder>"

// InteractiveModeNotice is the model-facing instruction appended to an
// interactive background-launch tool result. Wrapped in <system-reminder> so UI
// card renderers strip it automatically.
const InteractiveModeNotice = "<system-reminder>This is an INTERACTIVE background process (a long-running service or REPL). You may use terminal_output to inspect its logs and terminal_input to send it commands. After launching a service, verify it with an external check (e.g., `curl http://localhost:PORT` or `pgrep`) rather than polling terminal_output in a tight loop.</system-reminder>"

// TerminalTool is an agent.ToolExecutor that runs shell commands through the
// system shell (`sh -c` on macOS/Linux, PowerShell on Windows; see
// shellCommand). Stdout and stderr are combined and returned as the tool
// result. Non-zero exit codes are reported as extra metadata in the result
// text rather than as a tool error, so the LLM can see the failure output and
// adapt.
//
// The LLM-facing tool name is "terminal" — calling it "bash" would imply a
// hard /bin/bash dependency, but the executor shells out via the platform
// shell (the model is told which one via the environment context).
//
// mgr, when non-nil, is the BackgroundManager used for run_in_background
// launches; nil falls back to the process-wide default manager. The field
// exists so tests can inject an isolated manager.
type TerminalTool struct{ mgr *BackgroundManager }

// parseBackgroundMode interprets the run_in_background parameter, which is now
// an enum string ("async" / "interactive") rather than a boolean. A missing
// key or empty string means synchronous. Any other value (including the legacy
// boolean true) surfaces as a tool error so the model gets immediate feedback.
func parseBackgroundMode(input map[string]any) (mode BackgroundMode, useBg bool, err error) {
	raw, ok := input["run_in_background"]
	if !ok {
		return "", false, nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf(
			"terminal: run_in_background must be a string (\"async\" or \"interactive\"), got %T. "+
				"Boolean values are no longer accepted.", raw)
	}
	if s == "" {
		return "", false, nil
	}
	switch s {
	case "async":
		return BgModeAsync, true, nil
	case "interactive":
		return BgModeInteractive, true, nil
	default:
		return "", false, fmt.Errorf(
			"terminal: run_in_background must be either \"async\" or \"interactive\" (got %q). "+
				"Use \"async\" for one-shot tasks like tests/builds/installs, \"interactive\" for "+
				"long-running services or REPLs. Omit it to run synchronously.", s)
	}
}

func (t TerminalTool) managerFor(ctx context.Context) *BackgroundManager {
	return resolveBackgroundManager(ctx, t.mgr)
}

// parseTimeout reads the optional synchronous "timeout" (whole seconds). Missing
// → the default (TerminalTimeout). A non-number, a non-positive value, or a
// value past MaxTerminalTimeout is a tool error so the model gets immediate
// feedback rather than a silently clamped or ignored wait. Only meaningful on
// the synchronous path — a backgrounded command has no timeout.
func parseTimeout(input map[string]any) (time.Duration, error) {
	raw, ok := input["timeout"]
	if !ok {
		return TerminalTimeout, nil
	}
	// JSON numbers arrive as float64; tolerate the integer types too.
	var secs float64
	switch v := raw.(type) {
	case float64:
		secs = v
	case int:
		secs = float64(v)
	case int64:
		secs = float64(v)
	default:
		return 0, fmt.Errorf(
			"terminal: timeout must be a number of seconds (got %T). "+
				"Omit it for the default %s, or pass e.g. {\"timeout\": 300}.", raw, TerminalTimeout)
	}
	if secs <= 0 {
		return 0, fmt.Errorf(
			"terminal: timeout must be a positive number of seconds (got %v). "+
				"Omit it for the default %s.", secs, TerminalTimeout)
	}
	d := time.Duration(secs * float64(time.Second))
	if d > MaxTerminalTimeout {
		return 0, fmt.Errorf(
			"terminal: timeout %gs exceeds the %s maximum for a synchronous command. "+
				"For a command you expect to run longer, launch it with run_in_background:\"async\" "+
				"(a one-shot task whose result can wait) or \"interactive\" (a long-running service) "+
				"instead — those detach immediately and have no timeout.", secs, MaxTerminalTimeout)
	}
	return d, nil
}

// Definition returns the agent.ToolDefinition the LLM receives in the tools
// list. The JSON Schema describes a required "command" string and an optional
// "run_in_background" enum.
func (TerminalTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "terminal",
		Description: "Run a shell command (POSIX sh on macOS/Linux, PowerShell on Windows; see Shell in the environment) and return stdout+stderr. Prefer read_file/write_file/edit_file/glob/grep when they fit.\n\nPick the mode by lifecycle:\n- Default (sync): output comes back in this call. Timeout 120s, or set `timeout` up to 600. On timeout the command is KILLED (not moved to the background), so size `timeout` to the command. Use it for most builds, tests, installs and lints, and always when your next step needs the result. Never use `nohup` or shell `&` in a sync call: the child inherits the pipes and the call hangs until it exits.\n- run_in_background:\"async\": a one-shot task you expect to run well past a minute or two whose result can wait. Returns an id; a [BACKGROUND COMPLETED] notice arrives by itself. Never call terminal_output/terminal_input on it.\n- run_in_background:\"interactive\": a server, watcher or REPL you keep using this session. terminal_output (a snapshot) and terminal_input are allowed; check a service from outside (curl, pgrep) rather than polling.\n- detached:true: ONLY when the user wants the process to outlive octo (an ngrok tunnel, a standalone daemon). Untracked and never killed; returns the OS pid, output goes to log_file. Don't hand-roll nohup/setsid/&, and don't use it to dodge the timeout.\nasync and interactive processes are tracked and killed when the session ends. Inside a sub-agent all three are ignored and the command runs sync.\nThere is no terminal_list: each completion notice lists the other tasks still running. Output goes through pipes and may sit buffered; prefix `stdbuf -oL` for live logs. (A human can promote a running sync command from the UI; don't plan on it.)",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},
				"stdin": map[string]any{
					"type":        "string",
					"description": "Text piped to stdin, then EOF. Use it for text with quotes, backticks or $ (e.g. `gh pr create --body-file -`, `git commit -F -`) instead of putting it in the command string.",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Seconds to wait for a sync command before killing it (default 120, max 600; larger is rejected). Ignored with run_in_background/detached.",
				},
				"run_in_background": map[string]any{
					"type":        "string",
					"enum":        []string{"async", "interactive"},
					"description": "\"async\" (one-shot, auto-notified on completion) or \"interactive\" (service/REPL). Omit to run sync. Ignored inside a sub-agent. See the tool description.",
				},
				"detached": map[string]any{
					"type":        "boolean",
					"description": "Start a daemon that outlives octo (untracked, never killed). Only when the user wants that; see the tool description.",
				},
				"log_file": map[string]any{
					"type":        "string",
					"description": "Output file for a detached process (default ./nohup.out; relative to the working dir, ~ expanded).",
				},
			},
			"required": []string{"command"},
		},
	}
}

// Execute runs the command and returns combined output. A non-zero exit code
// is appended to the output as `[exit: <error>]` rather than being surfaced
// as an error, giving the LLM visibility into what went wrong.
//
// Internally this delegates to ExecuteStream with a nil progress callback so
// both code paths share the same exec/scanner pipeline — only the streaming
// behavior changes.
func (t TerminalTool) Execute(ctx context.Context, name string, input map[string]any) (agent.ToolResult, error) {
	return t.ExecuteStream(ctx, name, input, nil)
}

// ExecuteStream runs the command and forwards each output line to progress
// as it arrives, returning the full aggregated stdout+stderr at the end.
// progress may be nil — in that case the behaviour is identical to Execute.
//
// stdout and stderr are merged into a single stream so the LLM sees them in
// chronological order (the same way they'd appear in an interactive terminal).
// Scanner buffer cap is 1 MiB per line — commands that emit a single 10MB-
// long line will get their final line truncated, but the more usual case of
// many short lines is unaffected.
//
// Timeout: a synchronous command runs under `timeout` seconds (default
// TerminalTimeout, capped at MaxTerminalTimeout). On expiry it is killed and
// reaped and an error is returned with the output produced so far — it is NOT
// moved to the background. A human may still promote a running command from the
// UI (Ctrl+B / button) before the timeout to keep it as a background process.
func (t TerminalTool) ExecuteStream(
	ctx context.Context,
	_ string,
	input map[string]any,
	progress func(chunk string),
) (agent.ToolResult, error) {
	command, _ := input["command"].(string)
	if command == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf(
			"terminal: the required \"command\" argument was empty or missing. " +
				"Put the shell command in the tool's JSON arguments as the \"command\" field, " +
				`e.g. {"command": "go test ./..."}. Do not call this tool with empty arguments — ` +
				"if you have nothing to run, respond with text instead.")
	}
	stdinText, _ := input["stdin"].(string)

	// A sub-agent has no life beyond the turn that spawned it: it returns its
	// result to the parent and stops, so there is no later turn in which it could
	// collect a detached/backgrounded process's output. Letting it background a
	// command therefore (a) leaks the process's completion notice into the PARENT
	// conversation, unattributed, and (b) makes the sub-agent return a useless
	// "started in background" mid-state instead of the result it was asked for.
	// So every terminal call from inside a sub-agent runs synchronously: the
	// detached and run_in_background branches below are skipped, and the sync
	// timeout kills the command with an error rather than promoting it.
	subAgent := IsSubAgent(ctx)

	// Detached launch: a daemon that deliberately outlives octo. Not tracked, not
	// killed on exit — fire-and-forget. Checked before run_in_background so
	// detached wins if both are set.
	if det, _ := input["detached"].(bool); det && !subAgent {
		logFile, _ := input["log_file"].(string)
		pid, logPath, err := startDetached(ctx, command, logFile)
		if err != nil {
			return agent.ToolResult{Text: ""}, err
		}
		return agent.ToolResult{
			Text: fmt.Sprintf(
				"Started detached process (pid %d). It runs independently of octo and will NOT be tracked or stopped when this session ends — manage it yourself (e.g. `kill %d`). stdout+stderr → %s",
				pid, pid, logPath),
			UI: terminalUI(command, "running", fmt.Sprintf("detached, pid %d → %s", pid, logPath)),
		}, nil
	}

	bgMode, useBg, err := parseBackgroundMode(input)
	if err != nil {
		return agent.ToolResult{Text: ""}, err
	}

	// Background launch: detach, no timeout, return the id immediately. The
	// guard above still applies, so dangerous commands are blocked either way.
	// Skipped for sub-agents (see subAgent above) — the requested async/
	// interactive mode is ignored and the command runs synchronously instead.
	if useBg && !subAgent {
		id, err := t.managerFor(ctx).Start(ctx, command, bgMode)
		if err != nil {
			return agent.ToolResult{Text: ""}, err
		}
		var stdinWarn string
		if stdinText != "" {
			if werr := t.managerFor(ctx).WriteStdinAndClose(id, stdinText); werr != nil {
				stdinWarn = stdinWriteWarning(werr)
			}
		}
		notice := AsyncModeNotice
		if bgMode == BgModeInteractive {
			notice = InteractiveModeNotice
		}
		return agent.ToolResult{
			Text: fmt.Sprintf("Started %s background process %s.%s\n\n%s", bgMode, id, stdinWarn, notice),
			UI:   terminalUI(command, "running", fmt.Sprintf("%s background process %s", bgMode, id)),
		}, nil
	}

	// If a sub-agent asked to background/detach, we ignored that above and fall
	// through to the synchronous path. Surface a note so the sub-agent learns the
	// mode was dropped rather than silently getting different behavior than it
	// requested. Wrapped in <system-reminder> so UIs keep it out of the card.
	var bgIgnoredNote string
	if subAgent {
		detached, _ := input["detached"].(bool)
		if useBg || detached {
			bgIgnoredNote = "<system-reminder>run_in_background / detached is not available to a sub-agent — a sub-agent must finish within its turn, so the command was run synchronously instead. Do not request async, interactive, or detached from a sub-agent; hand any genuinely long-running command back to the parent.</system-reminder>\n\n"
		}
	}

	// Resolve the synchronous timeout up front so a bad value errors before we
	// start anything. Ignored on the background/detached paths (they returned above).
	syncTimeout, terr := parseTimeout(input)
	if terr != nil {
		return agent.ToolResult{Text: ""}, terr
	}

	// Synchronous path: start as a hidden background process so its output can be
	// collected as it streams; on timeout the process is killed and reaped (it is
	// NOT left running / promoted). We attach an onLine callback to collect output
	// and stream to progress in real time. The polling loop only checks status.
	//
	// The collector is capped at maxBgOutputBytes: a command that floods stdout
	// faster than the timeout (e.g. a runaway `yes`) would otherwise grow an
	// unbounded buffer and OOM the agent. Past the cap we keep the most recent
	// bytes and flag that earlier output was dropped — the same tail-retention
	// policy bgProcess.append uses for the background buffer.
	var (
		outMu   sync.Mutex
		out     []byte
		dropped bool
	)
	// Progress channel: decouple the scanner goroutine from the progress
	// consumer. A slow progress handler (e.g. a full WS events channel)
	// must never block the scanner — a blocked scanner backs up the
	// io.Pipe, which blocks Go's internal stdout-copy goroutine, which
	// prevents cmd.Wait() from returning (5-link deadlock).
	var progressCh chan string
	if progress != nil {
		progressCh = make(chan string, 128)
		go func() {
			// A panicking progress handler costs this command its live output —
			// senders drop lines once nobody is receiving (the select below
			// falls through to default) — but it must not cost the process.
			defer func() { _ = panics.Error(recover(), "terminal progress handler") }()
			for line := range progressCh {
				progress(line)
			}
		}()
		defer close(progressCh)
	}
	onLine := func(line string) {
		outMu.Lock()
		out = append(out, line...)
		if len(out) > maxBgOutputBytes {
			out = out[len(out)-maxBgOutputBytes:]
			dropped = true
		}
		outMu.Unlock()
		if progressCh != nil {
			trimmed := strings.TrimRight(line, "\n")
			select {
			case progressCh <- trimmed:
			default:
				// Drop when the consumer falls behind — better
				// than deadlocking the entire pipe chain.
			}
		}
	}

	mgr := t.managerFor(ctx)
	id, err := mgr.Start(ctx, command, BgModeAsync, WithOnLine(onLine), WithVisible(false))
	if err != nil {
		return agent.ToolResult{Text: ""}, err
	}
	var stdinWarn string
	if stdinText != "" {
		if werr := mgr.WriteStdinAndClose(id, stdinText); werr != nil {
			stdinWarn = stdinWriteWarning(werr)
		}
	}

	// Register a SyncSession so TUI (Ctrl+B) and Web ("Background" button) can
	// promote this process before the timer fires. A sub-agent's command is never
	// promotable: promotion leaves the process running past the sub-agent's turn
	// (see subAgent above), so we skip registration entirely — the process stays
	// invisible to HasActiveSync, so Ctrl+B/the button can't target it — and leave
	// promoteCh nil, so that select arm (a receive on a nil channel) never fires.
	var promoteCh <-chan struct{}
	if !subAgent {
		sess := mgr.BeginSync()
		defer mgr.EndSync()
		promoteCh = sess.C()
	}

	// snapshot returns the collected output so far, tab-expanded and with the
	// truncation marker prepended when the cap dropped earlier bytes. The
	// stdin-write warning (if any) is appended so every return path carries it.
	snapshot := func() string {
		outMu.Lock()
		body := strings.TrimRight(string(out), "\n")
		d := dropped
		outMu.Unlock()
		body = strings.ReplaceAll(body, "\t", "    ")
		if d {
			body = "[... earlier output truncated ...]\n" + body
		}
		return body + stdinWarn
	}

	// Poll until the process exits, the user promotes it, or the timeout fires.
	// Honour the caller's context deadline if it is sooner than the sync timeout.
	timeout := syncTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
			if timeout <= 0 {
				timeout = 1 * time.Millisecond
			}
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-promoteCh:
			// User promoted (Ctrl+B in TUI / button in Web): make the process
			// visible in the background panel and return. NOT reaped. Never fires
			// for a sub-agent (promoteCh is nil there).
			mgr.Promote(id)
			body := MaybeSpillOutput(id, snapshot())
			return agent.ToolResult{
				Text: fmt.Sprintf("%s\n\n[promoted to async background process %s]\n\n%s", body, id, AsyncModeNotice),
				UI:   terminalUI(command, "running", body),
			}, nil
		case <-timer.C:
			// Timeout: kill and reap the process, return the partial output plus an
			// error. The command is NOT moved to the background — a caller that wants
			// a long-running process asks for one explicitly (run_in_background), and
			// a human can still promote a slow one from the UI before this fires.
			mgr.Kill(id)
			body := snapshot()
			mgr.Remove(id)
			var advice string
			if subAgent {
				advice = "hand the long-running work back to the parent (a sub-agent can't background a command)"
			} else {
				advice = "re-run with a larger `timeout`, or launch it with run_in_background:\"async\" if its result can wait"
			}
			text := fmt.Sprintf("%s\n[timeout: command exceeded %s and was killed — it was NOT moved to the background. If you expect it to run longer, %s.]", body, timeout, advice)
			return agent.ToolResult{Text: text, UI: terminalUI(command, "failed", text)}, nil
		case <-ctx.Done():
			// User cancelled (Esc / Ctrl-C): kill the hidden process and reap
			// it — the output is returned here and now, nothing will poll it.
			mgr.Kill(id)
			body := snapshot()
			mgr.Remove(id)
			return agent.ToolResult{
				Text: body + "\n[exit: signal: killed]",
				UI:   terminalUI(command, "failed", body+"\n[exit: signal: killed]"),
			}, nil
		default:
		}

		_, status, _, _, _ := mgr.Read(id)
		if strings.HasPrefix(status, "exited") {
			body := MaybeSpillOutput(id, snapshot())
			// Reap the hidden process: its output has been captured and
			// returned, so the bgProcess (and its retained buffer) can go.
			mgr.Remove(id)
			hint := backtickSubstitutionHint(command, body)
			if status != "exited: 0" {
				text := bgIgnoredNote + hint + body + "\n[exit: " + strings.TrimPrefix(status, "exited: ") + "]"
				return agent.ToolResult{Text: text, UI: terminalUI(command, "failed", text)}, nil
			}
			return agent.ToolResult{Text: bgIgnoredNote + hint + body, UI: terminalUI(command, "success", body)}, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// backtickSubstitutionHint detects the classic failure where the model embeds
// markdown code spans (backticks) inside a double-quoted shell string — e.g.
// `gh pr create --body "use the `web_fetch` tool"`. The shell treats the
// backtick text as command substitution and tries to run it, producing
// "command not found" noise that the model misreads as a real failure (and
// silently drops the backticked text from the body). When both signals are
// present we prepend a corrective reminder so the model fixes it mid-session
// rather than self-rationalizing the errors. Wrapped in <system-reminder> so
// StripRemindersForDisplay keeps it out of the UI card.
func backtickSubstitutionHint(command, output string) string {
	// Shells word differently: bash/zsh say "command not found", dash (the
	// usual /bin/sh on Linux) says "<name>: not found". Match both.
	if !strings.Contains(command, "`") ||
		(!strings.Contains(output, "command not found") && !strings.Contains(output, ": not found")) {
		return ""
	}
	return "<system-reminder>The backticks (`) in your double-quoted shell string were executed by the shell as command substitution — that is what produced the \"command not found\" lines above; the command itself likely did NOT fail. If those backticks were meant as literal markdown code spans (e.g. in a PR/issue/commit body), re-run using the stdin parameter with `--body-file -` / `-F -` instead of embedding the text in --body.</system-reminder>\n\n"
}

// stdinWriteWarning formats a WriteStdinAndClose failure for the tool result.
// It is a warning rather than a hard error: a fast-exiting process that never
// reads stdin (or closes it early, like `head`) loses the race with the write,
// and the command's own output/exit status is the authoritative signal — but
// the model must still learn the text was not delivered.
func stdinWriteWarning(err error) string {
	return fmt.Sprintf("\n[warning: writing stdin failed: %v — the command ran without the stdin text]", err)
}

// terminalUI builds the "terminal" UI payload. The preview keeps the TAIL of
// the output — for shell commands the error/summary is at the bottom.
func terminalUI(command, status, output string) map[string]any {
	return map[string]any{
		"type":           "terminal",
		"command":        uiHead(command, 2, 200),
		"status":         status,
		"output_preview": uiTail(output, 16, 1200),
	}
}

// TerminalOutputTool reads new output (and status) from an INTERACTIVE
// background process launched with terminal run_in_background:"interactive".
type TerminalOutputTool struct{ mgr *BackgroundManager }

func (t TerminalOutputTool) managerFor(ctx context.Context) *BackgroundManager {
	return resolveBackgroundManager(ctx, t.mgr)
}

// Definition describes the required "id". Reading is non-destructive; to stop a
// process use the kill_shell tool.
func (TerminalOutputTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "terminal_output",
		Description: "Snapshot the last N lines and status (running/exited) of an INTERACTIVE background process. Read-only; stop it with kill_shell. Never use on async processes (wait for their notification) and don't loop on it: repeated empty snapshots are flagged as polling.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The background process id (e.g. \"bg_1\").",
				},
				"lines": map[string]any{
					"type":        "integer",
					"description": "How many trailing lines of output to return. Defaults to 50; use 0 for all retained output.",
				},
			},
			"required": []string{"id"},
		},
	}
}

// defaultTailLines is how many trailing lines terminal_output returns when the
// caller doesn't specify.
const defaultTailLines = 50

// Execute returns a snapshot of the process's last N lines plus a status line.
// Read-only and non-advancing — it never terminates the process (that's
// kill_shell) and never moves a read cursor, so it can't "block" and there is
// no polling incentive.
func (t TerminalOutputTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	id, _ := input["id"].(string)
	if id == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_output: id is required")
	}
	lines := defaultTailLines
	if v, ok := input["lines"].(float64); ok { // JSON numbers decode as float64
		lines = int(v)
	}
	mgr := t.managerFor(ctx)
	mode, ok := mgr.Mode(id)
	if !ok {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_output: no background process %q (it may have been reaped)", id)
	}
	if mode != BgModeInteractive {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_output: process %q is an async task; do not poll it. Wait for the [BACKGROUND COMPLETED] notification instead", id)
	}
	out, status, found, blocked, _ := mgr.Tail(id, lines)
	if !found {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_output: no background process %q (it may have been reaped)", id)
	}
	header := "[status: " + status + "]"
	if out == "" {
		msg := header + "\n(no output yet)"
		// A running process with nothing to show is, more often than not, the
		// pipe full-buffering trap: stdio block-buffers when not on a TTY, so
		// logs sit in the child's buffer. Teach the fix at the moment it bites.
		if status == "running" {
			msg += "\n(if this persists for a process that should be logging, its stdio is likely " +
				"block-buffered because it's piped — on macOS/Linux relaunch it as `stdbuf -oL <cmd>` " +
				"to force line buffering)"
		}
		// Hard stop for models that keep polling despite the "do not poll"
		// instructions. The agent loop's duplicate-tool-call detector would
		// eventually kill the turn; this surfaces the problem directly in the
		// tool result so the model sees why it must stop.
		if blocked {
			msg += "\n\n[STOP: repeated empty terminal_output calls detected. " +
				"Do not poll this process again. The system will push a [BACKGROUND COMPLETED] " +
				"notification when it finishes.]"
		}
		return agent.ToolResult{Text: msg}, nil
	}
	return agent.ToolResult{Text: header + "\n" + MaybeSpillOutput(id, out)}, nil
}

// TerminalInputTool sends text to the stdin of a running INTERACTIVE background
// process launched with terminal run_in_background:"interactive". Use to
// interact with long-running interactive applications (REPLs, configuration
// wizards, servers that accept commands via stdin).
type TerminalInputTool struct{ mgr *BackgroundManager }

func (t TerminalInputTool) managerFor(ctx context.Context) *BackgroundManager {
	return resolveBackgroundManager(ctx, t.mgr)
}

// Definition describes the required "id" and "input".
func (TerminalInputTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "terminal_input",
		Description: "Send text input to the stdin of a running INTERACTIVE background process launched with terminal run_in_background:\"interactive\". Use to interact with long-running interactive applications (e.g., REPLs, configuration wizards, servers that accept commands via stdin). You may NOT use terminal_input on async processes. The input is written verbatim — include a trailing newline (\\n) if the process expects line-based input.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The background process id (e.g. \"bg_1\").",
				},
				"input": map[string]any{
					"type":        "string",
					"description": "The text to send to the process's stdin. Include a trailing \\n if the process reads line-by-line.",
				},
			},
			"required": []string{"id", "input"},
		},
	}
}

// Execute writes input to the process's stdin. Unknown or exited id is an error.
// Reject async processes: terminal_input is only meaningful for interactive
// background tasks.
func (t TerminalInputTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	id, _ := input["id"].(string)
	if id == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_input: id is required")
	}
	text, _ := input["input"].(string)
	if text == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_input: input is required")
	}
	mgr := t.managerFor(ctx)
	mode, ok := mgr.Mode(id)
	if !ok {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_input: no background process %q", id)
	}
	if mode != BgModeInteractive {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_input: process %q is an async task; do not send input to it", id)
	}
	if err := mgr.WriteStdin(id, text); err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("terminal_input: %w", err)
	}
	return agent.ToolResult{Text: fmt.Sprintf("Sent to %s.", id)}, nil
}

// KillShellTool terminates a background process started by TerminalTool with
// run_in_background:"async" or "interactive" and returns its final output —
// the counterpart to terminal_output, which only reads. Split out from
// terminal_output's old kill:true flag so "stop this process" is a first-class,
// obvious action.
type KillShellTool struct{ mgr *BackgroundManager }

func (t KillShellTool) managerFor(ctx context.Context) *BackgroundManager {
	return resolveBackgroundManager(ctx, t.mgr)
}

// Definition describes the required "id".
func (KillShellTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "kill_shell",
		Description: "Stop a background (async or interactive) terminal process by id and return its final output. Use SIGTERM for servers/services so they release ports; SIGKILL (default) for one-shot tasks or when SIGTERM fails.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The background process id to terminate (e.g. \"bg_1\").",
				},
				"signal": map[string]any{
					"type":        "string",
					"enum":        []string{"SIGTERM", "SIGKILL", "SIGINT"},
					"description": "Signal to send. Defaults to SIGKILL. Use SIGTERM for graceful shutdown of servers and long-running services.",
				},
			},
			"required": []string{"id"},
		},
	}
}

// Execute kills the process, then returns its final remaining output. An
// unknown id is an error (Kill reports it); an already-exited process is a
// no-op kill and still returns its last output.
func (t KillShellTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	id, _ := input["id"].(string)
	if id == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("kill_shell: id is required")
	}
	sig, _ := input["signal"].(string)
	if sig == "" {
		sig = "SIGKILL"
	}
	mgr := t.managerFor(ctx)
	if !mgr.KillWithSignal(id, sig) {
		return agent.ToolResult{Text: ""}, fmt.Errorf("kill_shell: no background process %q", id)
	}
	// Give the process a moment to flush and the waiter to record exit.
	time.Sleep(50 * time.Millisecond)

	out, status, _, _, _ := mgr.Read(id) // found guaranteed: Kill succeeded
	header := "[killed] [status: " + status + "]"
	if out == "" {
		return agent.ToolResult{Text: header + "\n(no new output)"}, nil
	}
	return agent.ToolResult{Text: header + "\n" + MaybeSpillOutput(id, out)}, nil
}
