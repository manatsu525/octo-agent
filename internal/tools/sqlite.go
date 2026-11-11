package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/sqlitedb"
)

const (
	sqliteMaxBytes = 16 << 10 // of printed output
	sqliteMaxCell  = 1000     // bytes of one printed value
)

var sqliteLimits = sqlitedb.Limits{MaxRows: 200, MaxBytes: 1 << 20, Timeout: time.Minute}

// SQLiteTool runs one SQL statement against a named database under
// ~/.octo/databases/. It is the only writer that can create a database; pages
// (artifacts and Light Apps) query the same databases by name through
// ./__octo/db/<name> (internal/server/db_pages.go).
type SQLiteTool struct{}

func (SQLiteTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "sqlite",
		Description: "Run one SQL statement on a named SQLite database (`~/.octo/databases/<db>.db`, created on first use, persistent). Use for data collected over time (scheduled-task results, logs, data a page shows). Pages (artifacts, Light Apps) read the same db by name via `fetch('./__octo/db/<db>', {method:'POST', body: JSON.stringify({sql, params})})`, which returns `{columns, rows}` with rows as arrays in column order.\n\nOne statement per call (write many rows with multi-row VALUES or INSERT … SELECT); bind values with `?` and `params`; ATTACH and VACUUM INTO are refused. Queries print up to 200 tab-separated rows (use count(*) or LIMIT/OFFSET for more); writes print the changed count and last insert id. List dbs with glob `~/.octo/databases/*.db`; tables via `SELECT sql FROM sqlite_master`. In the conversation always use this tool, never `sqlite3` or scripts via terminal (it waits for locks); a script run outside octo may use its SQLite library with a lock wait (Python `sqlite3.connect(path, timeout=5)`).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"db": map[string]any{
					"type":        "string",
					"description": "Database name: 1-64 characters of a-z, 0-9, _ and -.",
				},
				"sql": map[string]any{
					"type":        "string",
					"description": "One SQL statement.",
				},
				"params": map[string]any{
					"type":        "array",
					"description": "Values for the `?` placeholders, in order: strings, numbers, booleans or null.",
					"items":       map[string]any{},
				},
			},
			"required": []string{"db", "sql"},
		},
	}
}

func (SQLiteTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	db := stringArg(input, "db")
	query := stringArg(input, "sql")
	if strings.TrimSpace(db) == "" {
		return agent.ToolResult{}, fmt.Errorf("sqlite: db is required")
	}
	var params []any
	if raw, ok := input["params"]; ok && raw != nil {
		list, ok := raw.([]any)
		if !ok {
			return agent.ToolResult{}, fmt.Errorf("sqlite: params must be an array")
		}
		params = list
	}
	res, err := sqlitedb.Exec(ctx, db, sqlitedb.Create, query, params, sqliteLimits)
	if err != nil {
		return agent.ToolResult{}, fmt.Errorf("sqlite: %w", err)
	}
	return agent.ToolResult{Text: formatSQLiteResult(res)}, nil
}

func formatSQLiteResult(res *sqlitedb.Result) string {
	if !res.HasRows {
		return fmt.Sprintf("changes=%d last_insert_id=%d", res.Changes, res.LastInsertID)
	}
	var b strings.Builder
	b.WriteString(strings.Join(res.Columns, "\t"))
	shown := 0
	for _, row := range res.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			cells[i] = sqliteCell(v)
		}
		line := strings.Join(cells, "\t")
		if b.Len()+1+len(line) > sqliteMaxBytes {
			break
		}
		b.WriteByte('\n')
		b.WriteString(line)
		shown++
	}
	switch {
	case len(res.Rows) == 0 && !res.Truncated:
		b.WriteString("\n(0 rows)")
	case shown < len(res.Rows) || res.Truncated:
		fmt.Fprintf(&b, "\n(showing the first %d rows; more not shown)", shown)
	}
	return b.String()
}

// sqliteCell renders one value on a single line so the tab-separated layout
// holds: NULL spelled out, tabs and line breaks escaped, a long value cut
// short so one cell cannot push its whole row out of the output.
func sqliteCell(v any) string {
	if v == nil {
		return "NULL"
	}
	s := fmt.Sprint(v)
	if len(s) > sqliteMaxCell {
		cut := sqliteMaxCell
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = fmt.Sprintf("%s…(%d bytes)", s[:cut], len(s))
	}
	return strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`).Replace(s)
}
