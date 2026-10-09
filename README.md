# lantern

A local dashboard for the notes and documents that [QMD](https://github.com/tobi/qmd) knows about.

lantern is a single Go binary with no external dependencies. It lets you:

- **browse and read** every QMD collection, folder by folder, with rendered markdown;
- **search** through your running QMD server, either Fast (keywords) or Smart (keywords plus meaning, reranked);
- **ask AI** about your documents. This page is a preview: AI answers are not implemented yet.

Everything runs on your computer. lantern only listens on `127.0.0.1`, reads QMD's config and your
markdown files without ever changing them, and does not use the internet.

> Screenshots: _coming soon_

## Prerequisites

1. **QMD** installed, with at least one collection:

   ```sh
   qmd collection add ~/notes --name notes
   qmd embed
   ```

2. **The QMD HTTP server** running. lantern uses it for search:

   ```sh
   qmd mcp --http --daemon      # starts it at http://localhost:8181
   qmd mcp stop                 # stops it
   ```

   Browsing and reading still work when QMD is not running. Only Search and Ask AI need it.

3. **Go 1.25 or newer**, only to build from source.

## Build and install

```sh
make build          # ./bin/lantern for this computer
make build-mac      # ./bin/lantern-darwin-arm64 (Apple Silicon)
make build-linux    # ./bin/lantern-linux-amd64
```

Copy the binary somewhere on your `PATH`:

```sh
cp bin/lantern-darwin-arm64 /usr/local/bin/lantern
# or
mkdir -p ~/bin && cp bin/lantern ~/bin/lantern
```

The binary has the templates, styles and scripts built in, so copying that one file is all you need.

## Usage

```sh
lantern
# lantern running at http://127.0.0.1:8090

lantern --open      # also opens the dashboard in your browser
```

Stop it with `Ctrl-C`.

| Page | What it does |
|---|---|
| **Library** | One card per collection, showing its description, document count, folder and last change. |
| **Collection** | A folder tree plus the documents in the current folder, with title, modified date and size. |
| **Document** | The rendered markdown, frontmatter chips and a table of contents. Buttons: *Open in editor*, *Copy path*, *Raw*. |
| **Search** | *Fast* searches as you type. *Smart* searches when you press Enter; the first Smart search can be slow while QMD loads its models. You can pick collections and how many results to show. |
| **Ask AI** | Coming soon. The page shows which provider keys lantern detected. |
| **Settings** | Read-only: the QMD server and its status, the config file, your collections, AI keys (masked) and the version. |

## Flags

Each flag also has an environment variable. A flag wins over its variable.

| Flag | Environment variable | Default |
|---|---|---|
| `--addr` | `LANTERN_ADDR` | `127.0.0.1:8090` |
| `--qmd-url` | `LANTERN_QMD_URL` | `http://localhost:8181` |
| `--qmd-config` | `LANTERN_QMD_CONFIG` | found the way QMD finds it (below) |
| `--qmd-index` | `LANTERN_QMD_INDEX` | default index (`index.yml`) |
| `--open` | — | `false` |
| `--version` | — | — |

When `--qmd-config` is not given, lantern looks for QMD's config in this order:

1. `$QMD_CONFIG_DIR/index.yml`
2. `$XDG_CONFIG_HOME/qmd/index.yml`
3. `~/.config/qmd/index.yml`

With `--qmd-index work` it reads `work.yml` instead of `index.yml`.

## Environment variables

| Variable | Purpose |
|---|---|
| `QMD_EDITOR_URI` | The *Open in editor* link, same as in QMD. Falls back to `editor_uri` in QMD's config, then to `vscode://file{path}`. |
| `LANTERN_ANTHROPIC_API_KEY` | Claude API key, for Ask AI (coming soon). |
| `LANTERN_OPENAI_API_KEY` | OpenAI API key, for Ask AI (coming soon). |
| `LANTERN_CLAUDE_MODEL` | Claude model. Default: `claude-opus-5-5`. |
| `LANTERN_OPENAI_MODEL` | OpenAI model. The default is picked when AI answers are added. |

lantern uses its own variable names on purpose. A global `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` in
your shell is also read by other tools, and lantern ignores those names. Put the keys in your shell
profile, for example `~/.zshrc`:

```sh
export LANTERN_ANTHROPIC_API_KEY="sk-ant-..."
export LANTERN_OPENAI_API_KEY="sk-..."
```

Keys are read when lantern starts. They are never stored, logged or sent to the browser.

## Security notes

- There is no login and no HTTPS. lantern is a personal tool and listens on `127.0.0.1` by default.
  If you change `--addr` to listen on your network, anyone who can reach it can read your notes, and
  lantern prints a warning.
- While listening on localhost, lantern rejects requests addressed to any other host name, which
  protects against DNS-rebinding attacks from websites.
- Markdown is rendered without raw HTML. Attachments such as images, PDFs and SVGs are served in a
  sandbox, so they can't run scripts.
- Hidden files and folders (like `.obsidian` or `.env`) are never listed or served.

## Development

```sh
make test     # go test ./...
make vet      # go vet ./...
make fmt      # gofmt
make run      # build and start
```

The build spec and the decisions made along the way are in [docs/BUILD_SPEC.md](docs/BUILD_SPEC.md).
