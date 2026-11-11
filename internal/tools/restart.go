package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
)

// activeRestarter, when non-nil, backs the restart_server tool and gates its
// advertisement in DefaultToolsFor. The server registers it (the restart
// goes through Server.Restart's drain); it stays nil in CLI/TUI processes,
// where there is no supervisor contract to honour. Within the server
// process the global is shared, so server sub-agents see the tool too —
// they inherit the parent's permission gate, which confirms interactively
// (browser modal on web, in-chat reply on IM) the same way it would for
// the parent.
var activeRestarter func(reason string)

// SetRestarter registers the function the restart_server tool delegates to.
// Pass nil to disable (the tool then doesn't appear in DefaultToolsFor).
// Mirrors SetAsker: process-global, set once at server start.
func SetRestarter(f func(reason string)) { activeRestarter = f }

func restarterEnabled() bool { return activeRestarter != nil }

// RestartServerTool lets the model restart the octo server process — after a
// binary upgrade or a config change that is only read at startup. The
// restart is scheduled, not immediate: the server drains in-flight turns
// (including the one executing this tool), so the model's final reply still
// reaches the user before the process exits and the supervisor respawns it.
type RestartServerTool struct{}

func (RestartServerTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "restart_server",
		Description: "Gracefully restart the octo server after replacing its binary or changing startup-only config (provider, model, system prompt, channels.yml). Runs after this turn: in-flight turns drain, then the supervisor respawns it. Tell the user it will be briefly unreachable (clients reconnect).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reason": map[string]any{
					"type":        "string",
					"description": "Short operator-facing reason for the restart, e.g. 'binary upgraded to 0.18.0' or 'config change: switched default model'. Logged on the server console.",
				},
			},
			"required": []string{"reason"},
		},
	}
}

func (RestartServerTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	restarter := activeRestarter
	if restarter == nil {
		return agent.ToolResult{}, fmt.Errorf("restart_server: not available in this mode (server only)")
	}
	reason := strings.TrimSpace(stringArg(input, "reason"))
	if reason == "" {
		return agent.ToolResult{}, fmt.Errorf("restart_server: reason is required")
	}

	restarter(reason)

	return agent.ToolResult{
		Text: "Restart scheduled: the server will drain in-flight turns and restart after this turn completes. " +
			"Clients reconnect automatically once the new process is up.",
		UI: map[string]any{"type": "restart", "reason": reason},
	}, nil
}
