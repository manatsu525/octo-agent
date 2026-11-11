# MCP (Model Context Protocol)

octo connects to MCP servers for extra tools — databases, ticket trackers, internal APIs. Tools are on by default; each configured server connects at session start and its tools ride alongside octo's built-ins as `mcp__<server>__<tool>`.

Declared in `~/.octo/mcp.json` (user-global):

```json
{
  "mcpServers": {
    "github": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-github"] },
    "linear": { "url": "https://mcp.linear.app/sse", "auth": "oauth" }
  }
}
```

`"auth": "oauth"` on a Streamable HTTP entry runs Authorization Code + PKCE automatically on first `401` — a browser tab opens for consent, the token caches at `~/.octo/mcp-tokens/<server>.json` and auto-refreshes. Check what's connected with `/mcp` in the TUI. A server that fails to start or times out (10s) is logged and skipped; the session continues with the others.

## Trimming a server's tools

A server can expose more tools (and longer schemas) than you use; every one rides along in each request. Two optional per-server keys in `mcp.json` trim what the model sees without changing how calls are made:

```json
"keenable": {
  "url": "https://api.keenable.ai/mcp",
  "tools": ["search_web_pages", "fetch_page_content"],
  "tool_overrides": {
    "search_web_pages": {
      "description": "Web search. Describe the ideal page.",
      "params": ["query", "published_after", "site"],
      "param_descriptions": {"query": "Natural-language description of the ideal page."}
    }
  }
}
```

`tools` is an allowlist of server tool names (add `resource_read` / `prompt_get` to keep those synthetic tools); calls to anything else are refused. `tool_overrides.<tool>` can replace the description, keep only the listed input `params` (dropped ones are simply never sent), and replace individual parameter descriptions. Restart the session (or `octo serve`) to apply.

## Tool Search

When MCP servers expose many tools, uploading every schema every turn wastes context. Tool Search keeps every connected tool's name + one-line description listed in the system prompt, and defers the full schema behind `mcp_describe` → `mcp_call`, loaded only on demand. Control it under `tools.tool_search` in `~/.octo/config.yml`:

```yaml
tools:
  tool_search:
    enabled: auto          # auto (default, activates past threshold_pct) | on | off
    threshold_pct: 10
```

Full reference (troubleshooting a broken server, the exact OAuth discovery flow): **https://octo-agent.dev/docs/guides/connect-mcp-servers/** (`web_fetch`).
