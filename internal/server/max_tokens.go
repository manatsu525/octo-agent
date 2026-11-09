package server

import (
	"os"
	"strconv"
	"strings"
)

// defaultServeMaxTokensEscalate mirrors the CLI's provider-aware default
// (cmd/octo: escalateMaxTokensOpenAI / escalateMaxTokensAnthropic, both
// 65536). Without it `octo serve` left Agent.MaxTokensEscalate at 0, which
// disabled BOTH truncation-recovery layers (escalated retry and prose
// resume): any reply that hit the provider's default output cap (32768) —
// e.g. a long-thinking reasoning model writing a large file — ended the turn
// with "Stopped: the response was truncated at the output-token cap".
// A model whose ceiling is lower backs off gracefully at the API
// (isMaxTokensTooLargeErr).
const defaultServeMaxTokensEscalate = 65536

// serveMaxTokensEscalate resolves the escalated output cap for server-built
// agents: OCTO_MAX_TOKENS_ESCALATE when set to a non-negative integer
// (0 disables escalation), else the default.
func serveMaxTokensEscalate() int {
	if env := strings.TrimSpace(os.Getenv("OCTO_MAX_TOKENS_ESCALATE")); env != "" {
		if n, err := strconv.Atoi(env); err == nil && n >= 0 {
			return n
		}
	}
	return defaultServeMaxTokensEscalate
}
