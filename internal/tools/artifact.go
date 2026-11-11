package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
)

// artifactContentTypes maps previewable extensions to the explicit
// Content-Type served for them. Shared by ShowArtifactTool (early validation
// with a useful error) and the server's artifact endpoint (response headers
// and final gate) so the two can't drift apart. It must also match the web
// client's EXT_KIND table (web/src/lib/artifacts.ts): a kind the client knows
// but the endpoint refuses makes the fetch 404 and the artifact silently
// vanish from the panel (#1895).
//
// Only kinds the panel can actually render belong here — a source or config
// file is not a deliverable, and the panel's Git Diff mode already shows the
// session's code changes with context. Listing them here would flood the
// artifact list with the routine bulk of a coding session and bury the
// reports, pages, and charts the panel exists for.
var artifactContentTypes = map[string]string{
	".html":     "text/html; charset=utf-8",
	".htm":      "text/html; charset=utf-8",
	".md":       "text/markdown; charset=utf-8",
	".markdown": "text/markdown; charset=utf-8",
	".png":      "image/png",
	".jpg":      "image/jpeg",
	".jpeg":     "image/jpeg",
	".gif":      "image/gif",
	".svg":      "image/svg+xml",
	".webp":     "image/webp",
}

// ArtifactContentType returns the Content-Type for a previewable artifact
// path, or ok=false when the extension isn't previewable.
func ArtifactContentType(path string) (ctype string, ok bool) {
	ctype, ok = artifactContentTypes[strings.ToLower(filepath.Ext(path))]
	return ctype, ok
}

func artifactExtList() string {
	exts := make([]string, 0, len(artifactContentTypes))
	for e := range artifactContentTypes {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	return strings.Join(exts, ", ")
}

// ShowArtifactTool surfaces an existing file to the user as a previewable
// artifact. write_file/edit_file payloads already feed the web Artifacts
// panel automatically; this tool covers files produced any other way —
// build scripts (e.g. web-artifacts-builder's bundle.html), generators,
// downloads. Its tool_use block also lands in the session transcript, which
// is what authorizes the artifact endpoint to serve the path.
type ShowArtifactTool struct{}

func (ShowArtifactTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "show_artifact",
		Description: "Present an existing previewable file (HTML, Markdown, image) to the user. ALWAYS call it right after producing one by any means other than write_file/edit_file (shell redirect, script, build, download); write_file/edit_file outputs appear automatically. Web UI: Artifacts panel; TUI: clickable link. Check the artifact-design skill before writing an HTML artifact.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path of the file to present. Previewable types: " + artifactExtList() + ".",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (ShowArtifactTool) Execute(_ context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	path := strings.TrimSpace(stringArg(input, "path"))
	if path == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("show_artifact: path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("show_artifact: %w", err)
	}
	if _, ok := ArtifactContentType(abs); !ok {
		return agent.ToolResult{Text: ""}, fmt.Errorf("show_artifact: %q is not a previewable type (want %s)", abs, artifactExtList())
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("show_artifact: %w", err)
	}
	if fi.IsDir() {
		return agent.ToolResult{Text: ""}, fmt.Errorf("show_artifact: %q is a directory", abs)
	}

	ui := map[string]any{"type": "artifact", "path": abs, "size_bytes": fi.Size()}
	return agent.ToolResult{
		Text: fmt.Sprintf("Artifact presented: %s (%d bytes)", abs, fi.Size()),
		UI:   ui,
	}, nil
}
