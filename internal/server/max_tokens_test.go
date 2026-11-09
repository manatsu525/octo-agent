package server

import "testing"

func TestServeMaxTokensEscalate(t *testing.T) {
	t.Setenv("OCTO_MAX_TOKENS_ESCALATE", "")
	if got := serveMaxTokensEscalate(); got != defaultServeMaxTokensEscalate {
		t.Fatalf("default = %d", got)
	}
	t.Setenv("OCTO_MAX_TOKENS_ESCALATE", "0")
	if got := serveMaxTokensEscalate(); got != 0 {
		t.Fatalf("explicit 0 = %d", got)
	}
	t.Setenv("OCTO_MAX_TOKENS_ESCALATE", "100000")
	if got := serveMaxTokensEscalate(); got != 100000 {
		t.Fatalf("explicit = %d", got)
	}
	t.Setenv("OCTO_MAX_TOKENS_ESCALATE", "junk")
	if got := serveMaxTokensEscalate(); got != defaultServeMaxTokensEscalate {
		t.Fatalf("junk = %d", got)
	}
}
