package server

import (
	"log/slog"

	"github.com/open-octo/octo-agent/internal/agent"
)

// recordTurnUsage folds a per-turn Agent's token counters into the session's
// persisted cumulative totals. Only valid for agents built fresh per turn
// (buildAgent), whose counters start at zero — the Web and scheduled-task
// paths. Best-effort: a save failure is logged and the turn goes on.
func recordTurnUsage(sess *agent.Session, a *agent.Agent) {
	if sess == nil || a == nil {
		return
	}
	in, out := a.SessionTokens()
	cr, cw := a.SessionCacheTokens()
	if err := sess.AddUsage(in, out, cr, cw); err != nil {
		slog.Warn("session: persist usage totals", "session_id", sess.ID, "err", err)
	}
}

// sessionUsagePayload returns the session's cumulative usage for API/WS
// payloads, or nil when nothing has been recorded (so the field is omitted).
func sessionUsagePayload(sess *agent.Session) *agent.UsageTotals {
	if sess == nil {
		return nil
	}
	u := sess.UsageSnapshot()
	if u.Turns == 0 && u.Input == 0 && u.Output == 0 && u.CacheRead == 0 && u.CacheWrite == 0 {
		return nil
	}
	return &u
}
