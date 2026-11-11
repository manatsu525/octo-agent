---
name: light-apps
catalog: "Read when you produce a reusable HTML page (offer to save as a Light App), build/edit a Light App or its data, or change the start screen/landing page or Web UI theme (轻应用, 首页, 主题)."
description: Light Apps (reusable token-free HTML pages under ~/.octo/light-apps), their named-database data path, the new-session start screen (~/.octo/landing/config.json, hero, app shortcuts) and Web UI themes. Use when you have produced an HTML page that could be reused, when the user asks to save/build/edit/mount a Light App, store an app's data, customize the start page or landing hero ("改首页", "开始页", "轻应用", "保存成应用"), or make a theme ("换主题", "做个主题").
---

# Light Apps

You can turn HTML artifacts into reusable **Light Apps** — HTML pages that users open anytime without consuming LLM tokens. When you generate an HTML page for the user, evaluate whether the task is REPEATABLE. If it is, proactively suggest saving it as a Light App.

## Storage convention

Light Apps live under `~/.octo/light-apps/<slug>/` with two files:

- `manifest.json` — metadata:
  ```json
  {"slug":"<slug>","name":"<display name>","description":"<one-line>","icon":"<emoji>","created_at":"<ISO-8601>"}
  ```
  Optional `"mount": "view"` gives the app a permanent place in the UI: its own page in the left navigation. Leave it out — the default — and the app lives on the Light Apps page, which is right for almost everything. Add it only when the user asks for one ("put it in the sidebar"). It is the only value: the right-hand panel belongs to the session (artifacts, diff), and an app is not part of a session

  Optional `"databases": ["<name>", …]` lists the named databases the page queries (see "Where a page keeps its data"). Always list them when the page uses any: if the user later makes the app public from the UI, a public app may read only the databases listed here
- `index.html` — the application. Other files it needs (scripts, styles, images, fonts, models, media) go in the same directory and are referenced by relative path

Create both files with `write_file`. No special tools needed.

## When to suggest saving as a Light App

- ✅ Repeated tasks: data reconciliation, format conversion, template tools, generators, worksheet/checklist tools, daily/weekly reports
- ✅ Tasks with well-defined input → output rules
- ✅ Tasks achievable with pure client-side HTML/CSS/JS (FileReader, `<input type="file">`, localStorage)
- ✅ Views over data collected over time — a scheduled task writes it into a named database, the app shows it (see "Where a page keeps its data")
- ❌ One-off research or analysis
- ❌ Tasks that genuinely need LLM reasoning each time
- ❌ Backend-dependent workflows (use a skill or workflow instead)

## How to save

1. Write the HTML with `write_file`; it opens in the Artifacts panel on its own
2. Ask the user whether to save it as a Light App: they can open it from the Light Apps panel anytime, and it uses no tokens
3. On confirmation: `write_file` to `~/.octo/light-apps/<slug>/manifest.json` and `~/.octo/light-apps/<slug>/index.html`
4. Choose a slug: lowercase letters, digits, hyphens. Derive from the app name.
5. Tell the user it is saved and can be opened from the Light Apps panel anytime

To mount an app the user already saved, edit that one field in its `manifest.json` — nothing else changes, and the entry appears on the next page load. Whenever you change an existing `manifest.json`, keep every field you did not mean to change; the user may have set some from the UI.

## Constraints on index.html

- The page is an ordinary web page shown in a frame, so browser features work: `localStorage` persists (each app's keys are kept apart from other apps and from octo's own; see "Where a page keeps its data" for what belongs there), `<a download>` saves, fullscreen and WebGL work, and `fetch` can reach any API. Do not call octo's own API from the page (`./__octo/db/` below is the page's own data path, not that API)
- Files in the app's directory load by relative path: `<script src="./app.js">`, `<link href="./style.css">`, `<img src="./chart.png">`, `./model.glb`, `./data.json`, fonts, audio, video, and other `.html` pages. Never start such a path with `/` — the app is served under a path prefix, and an absolute path lands outside it
- External scripts and stylesheets may come from any host. Pin exact versions. If the user is in mainland China, prefer a mirror that is reachable there (`cdn.bootcdn.net`, `cdn.staticfile.net`, `registry.npmmirror.com`). Reach for a CDN only when a real library (React, ECharts, Chart.js, three.js, …) is needed — a page that depends on one shows nothing when that host is unreachable
- Use `FileReader` + `<input type="file">` for file processing
- To let the user save a result (an image, a converted file, a CSV), use the standard download idiom: build a `Blob` (or `canvas.toDataURL()`), point an `<a download="name.ext">` at it and call `.click()` — no special API
- Form submit handlers must call `event.preventDefault()` — an unprevented submit reloads the app and drops its state
- Size the page to the frame, not to a pixel height you picked: the same app is shown in a ~420px sidebar, as a full-height mounted page, and as a landing `hero` frame that can be as short as ~300px. Let a flex column fill the height with the main region growing (keep a `min-height` on it) and drop the least important blocks under `@media (max-height: …)`. A main element pinned to a pixel height is clipped in any frame shorter than you assumed — in the hero that is the normal case
- octo loads the page with `?theme=dark` or `?theme=light` — the user's Web UI theme, which is not necessarily the OS's. Read it and key the palette off it, applying it before the body paints (an inline `<script>` in `<head>`); the frame reloads when the theme changes, so reading it once at load is enough. A page that only follows `prefers-color-scheme` becomes the one dark rectangle on a light screen
- Use emoji or inline SVG for icons
- Follow `artifact-design` skill conventions for layout and colors

## Where a page keeps its data

Decide by who the data belongs to:

- **`localStorage`** — what serves only this page in this browser: view state (selected tab, filters, sort order), drafts, remembered inputs, and in a public app each visitor's own state. Know its costs: it is stored per origin, so the desktop app, a browser on `localhost` and a phone through a tunnel each see their own separate copy; the agent cannot read it; it holds a few MB of strings with no querying; clearing site data wipes it
- **A named database** (the `sqlite` tool) — anything read by someone other than this page in this browser: data a scheduled task or the agent writes; data the user may later ask the agent about ("这个月记了多少账"); data that must show up on another device; data large enough to need `WHERE` / `GROUP BY`; and the user's own records that would hurt to lose — expenses, to-dos, notes, a reading log — even when the page is their only writer

When unsure: if the data may one day be read by anyone besides this page in this browser — the agent, a scheduled task, another device — use a named database.

A named database is not a file beside the page. The writer and the page meet at the database name only: a scheduled task never needs to know which page shows its data or where that page lives.

The page — an HTML artifact in the panel and the same page saved as a Light App alike — queries it by a relative path:

```js
const res = await fetch('./__octo/db/prices', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({sql: 'SELECT ts, price FROM quote WHERE symbol = ? ORDER BY ts', params: ['AAPL']}),
})
const data = await res.json() // {columns, rows, truncated} for a query; {changes, last_insert_id} for a write; {error} with a non-2xx status
// rows is an array of ARRAYS in `columns` order — [["2026-09-23T10:00:00Z", 231.4], …] — not objects:
// row.price is undefined. Index by position, or turn each row into an object first:
const items = data.rows.map(r => Object.fromEntries(data.columns.map((c, i) => [c, r[i]])))
```

- One statement per request, `?` placeholders with `params`. At most 10000 rows come back; aggregate in SQL rather than fetching everything
- The database must already exist — create it and its tables with the `sqlite` tool when you build the page, also for a page that is the data's only writer; a page cannot create one
- A page may write (add a record, mark a row read, delete a bad one) with `INSERT` / `UPDATE` / `DELETE` / `REPLACE`. Anything else — `CREATE`, `ALTER`, `DROP`, `PRAGMA` — is refused from a page: the table layout is set with the `sqlite` tool, and a scheduled task's SQL breaks if a page changes it
- A public Light App reads only — its writes are refused — so a page meant to be shared must work without writing
- Moving a page from an artifact to a Light App needs nothing for its data: the database stays where it is
- In the conversation, read and write databases with the `sqlite` tool, never `sqlite3` or a script through `terminal`: Windows has no `sqlite3`, and the tool waits for locks and refuses a second statement

A collecting job that needs no judgment on each run — fetch a URL, parse it, insert the rows — is better written as a script the OS scheduler runs (cron, launchd, Windows Task Scheduler) than as a scheduled task, which is an LLM turn every time and fires only while octo is running. Offer that when the user's job is deterministic. Such a script writes the database with its language's SQLite library:

- Create the database and its tables with the `sqlite` tool first, so the file starts in WAL mode; if the script creates it, it runs `PRAGMA journal_mode=WAL` once
- Open it at the path under `~/.octo/databases/` (the profile's data directory when octo runs with `--profile`) with a lock wait — `sqlite3.connect(path, timeout=5)` in Python — so it queues behind a page or octo instead of failing
- Use absolute paths for the interpreter, the script and its files: the OS scheduler's `PATH` and working directory are not the user's shell's. Keep third-party packages in a virtual environment
- Record every run (time, status, error) in a `runs` table and have the page show the latest one. A failed OS job is otherwise silent — octo neither sees it nor notifies anyone
- Registering the job (`crontab`, `launchctl`, `schtasks`) asks the user first; say what it will run and how often before you do

# The start screen

The new-session page comes from `~/.octo/landing/config.json` when it exists, and from octo's built-in set when it does not. Each card is `{icon, title, prompt}`; clicking one loads its prompt into the composer without sending, so a good prompt reads like the first thing the user would have typed. `icon` is an emoji, or an icon name like `ant-design:tool-outlined` — prefer an emoji, since octo carries its icons offline and a name it does not already bundle renders blank.

`hero` fills the space above the mark. It takes one source, not both: `"hero": {"image": "file.webp"}` for a file beside the config (animated GIF and WebP work), or `"hero": {"app": "<slug>"}` for one Light App embedded as a frame. An optional `"height"` is in pixels, clamped to 80–420; keep it modest (300–360) rather than at the cap, because the hero shares the screen with the cards and the composer and a taller one pushes the whole page past a short window — and the landing scrolls itself to the composer, so what gets cut off is the top of the hero. An app used as a hero must fill whatever height it is given (see the Light App constraints). `apps` is a list of Light App slugs shown as shortcuts under the cards.

Both hero sources fail silently, so get them right the first time. The image must be `.webp`, `.png`, `.jpg`, `.jpeg`, `.gif` or `.avif` — **an SVG is refused**, because it can carry script, so reach for PNG or WebP when you generate one. A `hero.app` or an `apps` entry that is not an installed Light App is simply not shown. In both cases the page renders without the hero and nothing reports an error, so check the file is there and the slug is installed rather than telling the user it is done.

When the user wants different starting points — "make the start page about my work", "put something in that empty space" — write that config, and put any image beside it in the same directory. It replaces all the cards rather than adding to them, up to 8, and whatever language it is written in is what shows. Deleting the directory restores the built-ins.

# Themes

The Web UI's palette is user-editable: a theme is `~/.octo/themes/<id>/` holding `manifest.json` and `theme.css`, picked up under Settings → Theme on the next page load. When the user asks for one, read `THEMES.md` in the product-help skill directory for the format and its traps (the light/dark two-block rule, gradient-only `--chat-bg`, absolute asset URLs) — or start from a theme octo ships in that directory, which carries the full variable set with comments.
