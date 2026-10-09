# lantern — Build Instructions (for Claude)

> **Working name:** `lantern` (rename freely; search-and-replace this file).
> **Status:** not started. This file is the build spec. Claude implements it phase by phase,
> after the user creates the GitHub repository.

---

## Rules for the implementer

- **Never run git.** The user does all git work. Leave every change uncommitted. Skip any
  "commit" step from skills or plans.
- **QMD is out of scope.** Do not install, configure, or modify QMD. It is a prerequisite.
- **Treat QMD data as read-only.** Never write to QMD's config file, its SQLite database, or the
  user's markdown files.
- **Verify library APIs before using them.** Fetch current docs (ctx7) for goldmark,
  anthropic-sdk-go, openai-go, etc. Do not code library calls from memory.
- **Work one phase at a time.** Each phase ends with its checklist. Stop and report after each
  phase. Do not start the next one until the user says so.
- **Do not implement Phase 6** (AI search) until the user explicitly asks. Phase 5 only builds its UI.
- **It's a personal local tool.** No auth and no TLS. Plain HTTP bound to `127.0.0.1`. Keep it simple.

---

## Goal

A **single Go binary** that serves a local web dashboard where the user can:

1. **Browse and read** all the markdown content that QMD knows about, organized by collection
   and folder, with rendered markdown.
2. **Search** that content through the running QMD server (keyword or hybrid search, all local).
3. See an **"Ask AI"** page for asking questions in natural language, answered by Claude or OpenAI
   using QMD as the retrieval tool. **Phase 5 builds only the UI. Phase 6 implements the AI.**

**UI wording:** the dashboard is about *content*. Don't use the words "index" or "indexes" in the
UI. Use "Library", "Collections", "Documents", "Search".

Typical use:

```sh
qmd mcp --http --daemon      # QMD running (prerequisite, not part of this project)
lantern                   # start the dashboard
# → lantern running at http://127.0.0.1:8090
```

---

## Prerequisites (already done by the user, out of scope)

- QMD installed. Collections added with `qmd collection add`, embeddings generated with `qmd embed`.
- QMD HTTP server running: `qmd mcp --http --daemon` (default `http://localhost:8181`).
  Stop it with `qmd mcp stop`.
- Go installed (latest stable). Set the `go` directive in `go.mod` accordingly.

---

## Architecture

```
 Browser (http://127.0.0.1:8090)
        │
        ▼
 lantern (Go binary, single process)
   ├── reads QMD config YAML ─────────► ~/.config/qmd/index.yml   (collections: name, path, pattern, ignore, context)
   ├── reads markdown from disk ──────► the collection folders     (browse + render documents)
   ├── calls QMD HTTP server ─────────► http://localhost:8181      (GET /health, POST /query)
   └── (Phase 6) calls an LLM API ────► Claude or OpenAI           (tool-use loop backed by QMD search)
```

### Data sources and why

| Need | Source | Why |
|---|---|---|
| List of collections, their folders, descriptions | QMD config YAML (`index.yml`) | Documented, user-editable format. `qmd collection list` and `qmd ls` only print human-readable text. |
| Files in each collection, file contents | Filesystem (walk `path` with `pattern` and `ignore`) | Files are plain markdown on disk. No need to go through QMD to read them. |
| Search | QMD HTTP `POST /query` | Real QMD search (BM25 + vectors + reranking) |
| QMD status | QMD HTTP `GET /health` | Liveness and uptime |

**Do not read QMD's SQLite database directly.** Its schema is internal and may change.

### QMD config file location

Resolve it the same way QMD does:

1. `--qmd-config` flag, if given.
2. `$QMD_CONFIG_DIR/index.yml`
3. `$XDG_CONFIG_HOME/qmd/index.yml`
4. `~/.config/qmd/index.yml`

A named QMD index (`--qmd-index work`) uses `work.yml` instead of `index.yml`.

Relevant YAML shape. Parse only what is needed and ignore unknown keys:

```yaml
global_context: "Knowledge base for my projects"
editor_uri: "vscode://file{path}:{line}:{col}"
collections:
  notes:
    path: /Users/me/notes
    pattern: "**/*.md"          # default when missing
    ignore: ["Archive/**", "**/drafts/**"]
    includeByDefault: true      # default true; false = only searched when named explicitly
    context:                    # path prefix → description; longest match wins
      "/": "Personal notes and ideas"
      "/work": "Work-related notes"
```

### QMD HTTP API (known parts)

- `GET /health` → `{"uptime": <number>}`
- `POST /query` (alias `/search`). There is **no** plain `q` parameter. The body needs `searches`:

```json
{
  "searches": [
    { "type": "lex", "query": "deploy" },
    { "type": "vec", "query": "deploy" }
  ],
  "collections": ["notes"],
  "limit": 20,
  "rerank": true
}
```

  - `searches[].type`: `lex` (BM25 keywords), `vec` (semantic), `hyde` (hypothetical document).
    1–10 entries. The first gets 2× weight.
  - `collections`: plural array only. A singular `collection` is silently ignored.
  - `rerank`: LLM reranking. Default true. False is faster.
  - Optional: `intent`, `minScore`, `candidateLimit`, `filter` (metadata filter).
- Results identify files as `qmd://<collection>/<relative/path>` and carry a docid
  (`#abc123`) and a `score`.
- **The full response schema is not documented.** Phase 0 captures a real response as a fixture.
  Model the Go types on that fixture.

### Mapping a search result to a file on disk

`qmd://<collection>/<rel/path>` → `collections[<collection>].path` + `/` + `<rel/path>`.
Clean the joined path and check it stays inside the collection root before reading it.

---

## Tech stack

All dependencies must be pure Go (no cgo), so the binary cross-compiles to macOS from Linux.

| Concern | Choice |
|---|---|
| HTTP server | stdlib `net/http` (Go 1.22+ routing patterns, e.g. `GET /c/{name}/{path...}`) |
| Templates | stdlib `html/template` |
| Static assets | stdlib `embed`. Everything is inside the binary. |
| Interactivity | htmx, **vendored** into `web/static/` (no CDN; works offline) plus a little vanilla JS |
| Markdown | `github.com/yuin/goldmark` with GFM, plus frontmatter (`goldmark-meta`) and syntax highlighting (`goldmark-highlighting`, chroma) |
| YAML | `gopkg.in/yaml.v3` |
| Globs | `github.com/bmatcuk/doublestar/v4` (supports `**`) |
| Tests | stdlib `testing` + `net/http/httptest`, table-driven |
| Phase 6 only | `github.com/anthropics/anthropic-sdk-go`, `github.com/openai/openai-go` |

No Node, no frontend build step.

---

## Project layout

```
lantern/
├── cmd/lantern/main.go     # flags, wiring, start server, optional --open
├── internal/
│   ├── config/                # app config from flags + env
│   ├── qmdconfig/             # locate + parse QMD's index.yml (read-only)
│   ├── library/               # walk collections, file tree, read files, titles, qmd:// resolution
│   ├── qmd/                   # QMD HTTP client: Health, Query
│   ├── render/                # markdown → HTML (goldmark), TOC, frontmatter
│   ├── ai/                    # Provider interface, claude + openai (stubs until Phase 6)
│   └── server/                # routes, handlers, template rendering
├── web/
│   ├── templates/             # layout + page templates
│   └── static/                # app.css, app.js, htmx.min.js (vendored)
├── testdata/                  # sample index.yml, sample markdown tree, QMD response fixtures
├── Makefile
├── go.mod
└── README.md                  # user-facing README (written in Phase 5)
```

---

## Configuration

Flags, each with an environment variable alternative:

| Flag | Env var | Default |
|---|---|---|
| `--addr` | `LANTERN_ADDR` | `127.0.0.1:8090` |
| `--qmd-url` | `LANTERN_QMD_URL` | `http://localhost:8181` |
| `--qmd-config` | `LANTERN_QMD_CONFIG` | resolved as described above |
| `--qmd-index` | `LANTERN_QMD_INDEX` | *(empty = default index)* |
| `--open` | — | `false` (open the browser on start) |

### AI provider keys (used in Phase 6, shown in UI from Phase 5)

**Decision: environment variables with app-specific names.** The program never stores keys,
and the browser never sees them.

| Env var | Purpose | Default |
|---|---|---|
| `LANTERN_ANTHROPIC_API_KEY` | Claude API key | — |
| `LANTERN_OPENAI_API_KEY` | OpenAI API key | — |
| `LANTERN_CLAUDE_MODEL` | Claude model | `claude-opus-5-5` |
| `LANTERN_OPENAI_MODEL` | OpenAI model | choose in Phase 6 from current OpenAI docs (don't guess) |

Why app-specific names instead of `ANTHROPIC_API_KEY` / `OPENAI_API_KEY`: a global
`ANTHROPIC_API_KEY` in the shell profile is also picked up by Claude Code and other tools, which
could switch them to API billing. Pass the key to each SDK client explicitly.

The user puts them in `~/.zshrc`:

```sh
export LANTERN_ANTHROPIC_API_KEY="sk-ant-..."
export LANTERN_OPENAI_API_KEY="sk-..."
```

---

## Pages and UI

Navigation bar: **Library · Search · Ask AI · Settings**. On the right, a status pill:
"QMD connected" / "QMD not running".

General:
- Clean, readable design. Light and dark themes (follow `prefers-color-scheme`, plus a toggle
  remembered in `localStorage`). Works on a narrow window.
- If QMD is not running, show a banner: *"QMD is not running. Start it with
  `qmd mcp --http --daemon`."* Library and documents **still work** without QMD, because they read
  the config and the filesystem. Only Search and Ask AI need it.

| Route | Page | Content |
|---|---|---|
| `GET /` | **Library** | One card per collection: name, context description, document count, folder path, last-modified date. Global context at the top if set. |
| `GET /c/{name}/` and `GET /c/{name}/{path...}` (folder) | **Collection** | Collapsible folder tree on the left. List of documents on the right: title (frontmatter `title`, else first `# H1`, else filename), modified date, size. Breadcrumbs. |
| `GET /c/{name}/{path...}` (file) | **Document** | Rendered markdown. Frontmatter as metadata chips. Table of contents from headings. Buttons: "Open in editor" (from `editor_uri`, default `vscode://file{path}`), "Copy path", "Raw" toggle. |
| `GET /search` | **Search** | Query box. Mode: **Fast** (`lex` only, `rerank: false`) or **Smart** (`lex` + `vec`, `rerank: true`). Collection multi-select, pre-selecting the `includeByDefault` collections. Result limit. Results: title, collection badge, relative path, score bar, snippet. Click opens the Document page. Live updates with htmx. Smart mode can be slow the first time (QMD loads its models). Show a spinner. |
| `GET /ask` | **Ask AI** | Provider selector (Claude / OpenAI), each with a "key detected" / "key missing" badge. Collection multi-select. Question textarea. "Ask" button. Answer area and a "Sources" list. **Until Phase 6:** the button is disabled, a banner says *"Coming soon — AI answers are not implemented yet"*, and both areas show empty states. |
| `POST /api/ask` | API | **Until Phase 6:** returns `501 Not Implemented` with `{"error":"not implemented"}`. |
| `GET /settings` | **Settings** (read-only) | QMD URL and health/uptime. QMD config path in use. Collections found. AI providers: key detected (masked, last 4 chars) and model. App version. |

---

## Phases

Each phase ends with: `go vet ./...` clean, `go test ./...` passing, and the manual checks
listed. Report results honestly, including failures.

### Phase 0 — Scaffold and discovery

1. `go mod init github.com/<user>/lantern`. Ask the user for the exact module path.
2. Create the layout above with a minimal `main.go` that starts and prints the URL.
3. **Capture real QMD responses** with QMD running (ask the user for permission if needed):
   - `curl -s http://localhost:8181/health` → `testdata/qmd_health.json`
   - `curl -s -X POST http://localhost:8181/query -H 'Content-Type: application/json' -d '{"searches":[{"type":"lex","query":"<a word from the user's notes>"}],"limit":5}'`
     → `testdata/qmd_query_lex.json`
   - The same with `lex` + `vec` and `rerank: true` → `testdata/qmd_query_smart.json`
   - Write down the actual fields found (file URI, docid, score, title, snippet/context, line…)
     in a comment at the top of `internal/qmd/types.go`.
   - Before committing them as fixtures, replace any private content with dummy text.
4. Makefile targets: `build`, `run`, `test`, `vet`, `fmt`, `build-mac` (`GOOS=darwin GOARCH=arm64`),
   `build-linux` (`GOOS=linux GOARCH=amd64`). Set `CGO_ENABLED=0`.

**Done when:** `make build` produces `./bin/lantern`, it starts and prints its URL, and the
fixtures exist.

### Phase 1 — Data layer (no UI)

1. `qmdconfig`: config path resolution (flag → env vars → default), YAML types, defaults
   (`pattern: **/*.md`, `includeByDefault: true`), and a "longest prefix wins" context lookup.
   Tests use `testdata/index.yml`.
2. `library`:
   - walk a collection with `pattern` + `ignore` (doublestar);
   - build the folder tree;
   - list documents with title, modified time and size;
   - read a file;
   - resolve `qmd://` URIs to disk paths, with the containment check.
   Tests use a temporary directory tree.
3. `qmd` client:
   - `Health(ctx)` and `Query(ctx, QueryRequest)`;
   - helpers to build Fast and Smart requests;
   - timeouts: 5 s for health, 120 s for query, because Smart can be slow on the first run.
   Tests use `httptest` and the Phase 0 fixtures.

**Done when:** unit tests cover config parsing, the walking and ignore rules, URI resolution
(including a `../` traversal attempt), and the client against the fixtures.

### Phase 2 — Server and layout

1. Router with Go 1.22 patterns. Templates and static files embedded via `embed`.
2. Base layout: nav, theme toggle, QMD status pill (checks `/health`, cached a few seconds),
   "QMD not running" banner, and 404/500 pages.
3. Wire up the flags and env vars. `--open` opens the browser (`open` on macOS, `xdg-open` on Linux).
4. Graceful shutdown on Ctrl-C.

**Done when:** the binary serves the layout, the pill and banner react to QMD being stopped
and started, and the theme toggle works.

### Phase 3 — Library and documents

1. Library page (collection cards).
2. Collection page (tree + document list + breadcrumbs).
3. Document page:
   - goldmark rendering (GFM tables, task lists, code highlighting, frontmatter chips, heading
     anchors + TOC);
   - relative links between markdown files resolve to other Document pages;
   - relative images are served from the collection folder (same containment check);
   - "Open in editor", "Copy path", "Raw".

**Done when:** the user can click from the Library down to any document and read it, rendered
nicely, in both themes.

### Phase 4 — Search

1. Search page with Fast/Smart mode, collection filter, limit.
2. htmx partial for results (`GET /search/results?q=...`). Debounce typing in Fast mode. In Smart
   mode, search only on Enter or button press.
3. Result → Document page link, using the `qmd://` resolution.
4. Error states: QMD down, empty results, timeout.

**Done when:** searching from the dashboard returns the same documents as
`qmd query --json "<same query>"` on the CLI, for both modes.

### Phase 5 — Ask AI page (UI only), Settings, provider stubs, user README

1. `internal/ai`:

   ```go
   var ErrNotImplemented = errors.New("not implemented")

   type AskRequest struct {
       Question    string
       Collections []string
   }

   type Source struct {
       Collection string
       Path       string // relative path inside the collection
       Title      string
   }

   type AskResponse struct {
       AnswerMarkdown string
       Sources        []Source
   }

   type Provider interface {
       ID() string            // "claude" | "openai"
       DisplayName() string   // "Claude" | "OpenAI"
       Configured() bool      // API key env var present
       Model() string
       Ask(ctx context.Context, req AskRequest) (AskResponse, error) // returns ErrNotImplemented until Phase 6
   }
   ```

   Implement `claude` and `openai` providers. Both read their env vars, and both return
   `ErrNotImplemented` from `Ask`. **Do not add the SDK dependencies yet.**
2. Ask AI page exactly as described in **Pages and UI**: selector with key badges, disabled button,
   "Coming soon" banner. `POST /api/ask` → 501.
3. Settings page (read-only), as described above.
4. Write the **user-facing `README.md`**: what it is, prerequisites (QMD running), build/install
   (`make build`, `make build-mac`, copy the binary to `/usr/local/bin` or `~/bin`), usage, flags,
   env vars, screenshots placeholder. Move this spec to `docs/BUILD_SPEC.md`.

**Done when:** every page renders, the Ask AI page clearly shows the feature as coming soon, the
key badges reflect the env vars, and `/api/ask` returns 501.

---

### Phase 6 — (FINAL, LATER) AI search with Claude and OpenAI

> **Do not implement until the user asks.** Before starting: load the `claude-api` skill and fetch
> current OpenAI Go SDK docs (ctx7). Don't rely on remembered SDK APIs or model names.

**Idea:** the LLM gets tools backed by QMD and the filesystem. It decides what to search, reads
the relevant documents, and writes an answer that cites its sources. Retrieval stays local. Only
the snippets and documents the model reads are sent to the provider.

#### Shared design (provider-independent)

- Tools, with one JSON Schema definition shared by both providers:
  - `search_documents(query: string, mode: "fast"|"smart", collections?: string[], limit?: number)`
    → QMD `POST /query`. Return a compact list: collection, path, title, score, snippet.
  - `read_document(collection: string, path: string, from_line?: number, max_lines?: number)`
    → read from disk (containment check). Cap the size, e.g. 400 lines per call.
  - `list_collections()` → names + context descriptions from the QMD config.
- The tool executor is plain Go code, independent of the provider. Each provider only translates
  tool calls and results.
- System prompt:
  - answer only from the documents;
  - say clearly when nothing relevant was found;
  - cite sources as `collection/path`;
  - stay inside the collections the user selected, which the server passes in.
- Loop guard: at most ~8 tool rounds per question, and an overall timeout.
- Response: answer markdown (rendered with the same goldmark renderer) + `Sources` (every
  document the model read via `read_document`, plus search hits it cited).
- Optional: stream the answer to the browser with Server-Sent Events.

#### Claude specifics

- SDK `github.com/anthropics/anthropic-sdk-go`. Create the client with an explicit API key from
  `LANTERN_ANTHROPIC_API_KEY`.
- Default model `claude-opus-5-5`, configurable via `LANTERN_CLAUDE_MODEL`.
- Tool loop: the SDK's beta tool runner (`toolrunner.NewBetaToolFromJSONSchema` +
  `client.Beta.Messages.NewToolRunner`; a streaming variant exists). Confirm exact signatures in
  the skill docs.
- On this model:
  - thinking is always on (leave it unset or adaptive);
  - set `effort` explicitly in `OutputConfig` (the model's default is `medium`) and make it
    configurable;
  - use `tool_choice` `auto` only, because forced `any`/`tool` returns 400;
  - check `stop_reason == "refusal"` before reading content;
  - enable server-side refusal fallbacks as the skill describes.

#### OpenAI specifics

- SDK `github.com/openai/openai-go`. Create the client with an explicit API key from
  `LANTERN_OPENAI_API_KEY`.
- Check the current docs to choose the API style (function/tool calling) and the default model.
  Make the model configurable via `LANTERN_OPENAI_MODEL`.
- Same three tools, same executor, same loop guard.

#### UI changes

- Enable the Ask button for providers whose key is detected. Keep the badge for missing keys.
- Show progress ("Searching…", "Reading notes/x.md…"), then the answer and clickable sources
  that open the Document page.
- Show errors clearly (missing key, provider error, QMD down).

**Done when:** the same question works with both providers. Answers cite documents that exist
and open correctly. With no key set, the page still shows "key missing" instead of failing.

---

## Out of scope (for now)

- Installing or configuring QMD, or editing collections from the UI.
- Triggering `qmd update` / `qmd embed` from the UI. Could be a later addition.
- Auth, HTTPS, multi-user, deployment.
