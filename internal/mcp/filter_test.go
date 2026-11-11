package mcp

import (
	"encoding/json"
	"testing"
)

func TestToolFilter_AllowlistAndOverrides(t *testing.T) {
	f := newToolFilter(ServerEntry{
		Tools: []string{"search", "resource_read"},
		ToolOverrides: map[string]ToolOverride{"search": {
			Description:       "short",
			Params:            []string{"query", "site"},
			ParamDescriptions: map[string]string{"query": "q"},
		}},
	})
	in := []Tool{
		{Name: "search", Description: "long", InputSchema: json.RawMessage(`{"type":"object","title":"x","properties":{"query":{"type":"string","description":"long q"},"site":{"type":"string"},"session_id":{"type":"string"}},"required":["query","session_id"]}`)},
		{Name: "feedback", Description: "unused"},
	}
	out := f.apply(in)
	if len(out) != 1 || out[0].Name != "search" || out[0].Description != "short" {
		t.Fatalf("unexpected tools: %+v", out)
	}
	var schema map[string]any
	if err := json.Unmarshal(out[0].InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["session_id"]; ok || len(props) != 2 {
		t.Errorf("params not trimmed: %v", props)
	}
	if d := props["query"].(map[string]any)["description"]; d != "q" {
		t.Errorf("param description not overridden: %v", d)
	}
	if req := schema["required"].([]any); len(req) != 1 || req[0] != "query" {
		t.Errorf("required not trimmed: %v", req)
	}
	if _, ok := schema["title"]; ok {
		t.Errorf("title should be dropped")
	}
	if !f.Allows("resource_read") || f.Allows("prompt_get") || f.Allows("feedback") {
		t.Errorf("Allows mismatch")
	}
	// Original slice untouched.
	if in[0].Description != "long" {
		t.Errorf("input mutated")
	}
}

func TestToolFilter_NilExposesAll(t *testing.T) {
	var f *toolFilter = newToolFilter(ServerEntry{})
	if f != nil {
		t.Fatal("empty entry should give nil filter")
	}
	in := []Tool{{Name: "a"}, {Name: "b"}}
	if got := f.apply(in); len(got) != 2 || !f.Allows("anything") {
		t.Errorf("nil filter should pass everything through")
	}
}
