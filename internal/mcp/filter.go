package mcp

import "encoding/json"

// toolFilter is the model-facing view of one server's tools as configured in
// mcp.json (ServerEntry.Tools / ToolOverrides). nil means expose everything
// verbatim.
type toolFilter struct {
	allow     map[string]bool
	overrides map[string]ToolOverride
}

func newToolFilter(e ServerEntry) *toolFilter {
	if len(e.Tools) == 0 && len(e.ToolOverrides) == 0 {
		return nil
	}
	f := &toolFilter{overrides: e.ToolOverrides}
	if len(e.Tools) > 0 {
		f.allow = map[string]bool{}
		for _, t := range e.Tools {
			f.allow[t] = true
		}
	}
	return f
}

// Allows reports whether the named tool (a server tool, or the synthetic
// "resource_read" / "prompt_get") is exposed.
func (f *toolFilter) Allows(name string) bool {
	return f == nil || f.allow == nil || f.allow[name]
}

func (f *toolFilter) apply(in []Tool) []Tool {
	if f == nil {
		return in
	}
	out := make([]Tool, 0, len(in))
	for _, t := range in {
		if !f.Allows(t.Name) {
			continue
		}
		if ov, ok := f.overrides[t.Name]; ok {
			t = ov.applyTo(t)
		}
		out = append(out, t)
	}
	return out
}

func (o ToolOverride) applyTo(t Tool) Tool {
	if o.Description != "" {
		t.Description = o.Description
	}
	if len(o.Params) == 0 && len(o.ParamDescriptions) == 0 {
		return t
	}
	var schema map[string]any
	if len(t.InputSchema) == 0 || json.Unmarshal(t.InputSchema, &schema) != nil {
		return t
	}
	props, _ := schema["properties"].(map[string]any)
	if len(o.Params) > 0 && props != nil {
		keep := map[string]bool{}
		for _, p := range o.Params {
			keep[p] = true
		}
		for k := range props {
			if !keep[k] {
				delete(props, k)
			}
		}
		if req, ok := schema["required"].([]any); ok {
			var kept []any
			for _, r := range req {
				if s, _ := r.(string); keep[s] {
					kept = append(kept, r)
				}
			}
			if len(kept) == 0 {
				delete(schema, "required")
			} else {
				schema["required"] = kept
			}
		}
	}
	for k, d := range o.ParamDescriptions {
		if p, ok := props[k].(map[string]any); ok {
			p["description"] = d
		}
	}
	for _, p := range props {
		if m, ok := p.(map[string]any); ok {
			delete(m, "title") // pydantic noise; the property key already names it
		}
	}
	delete(schema, "title")
	if b, err := json.Marshal(schema); err == nil {
		t.InputSchema = b
	}
	return t
}
