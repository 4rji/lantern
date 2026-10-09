# Quickstart

1. Add your notes to QMD and build the search index:

   ```sh
   qmd collection add ~/notes --name notes
   qmd embed
   ```

2. Start the QMD server:

   ```sh
   qmd mcp --http --daemon
   ```

3. Build lantern and install it:

   ```sh
   make build-mac                                  # Linux: make build
   cp bin/lantern-darwin-arm64 /usr/local/bin/lantern
   ```

4. Optional, for Ask AI: add your keys to `~/.zshrc`, then open a new terminal:

   ```sh
   export LANTERN_ANTHROPIC_API_KEY="sk-ant-..."
   export LANTERN_OPENAI_API_KEY="sk-..."
   ```

5. Run it:

   ```sh
   lantern --open
   ```

   The dashboard opens at http://127.0.0.1:8090. Stop it with `Ctrl-C`.

6. Stop QMD when you're done:

   ```sh
   qmd mcp stop
   ```

For flags and details, see [README.md](README.md).
