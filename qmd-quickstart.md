# QMD Quickstart: search your notes locally

[QMD](https://github.com/tobi/qmd) is a search engine for your markdown notes that runs on your own computer. It combines keyword search, semantic search and local AI reranking. Here's how to try it in a few minutes.

## 1. Install

```sh
npm install -g @tobilu/qmd
# or
bun install -g @tobilu/qmd
```

## 2. Create some demo notes

```sh
mkdir -p ~/qmd-demo/{work/deploy,ideas}
cd ~/qmd-demo

cat > README.md <<'EOF'
# Demo notes
Small collection to test QMD. See [deploy guide](work/deploy/guide.md).
EOF

cat > work/deploy/guide.md <<'EOF'
---
title: Deploy guide
tags: [ops, deploy]
---
# How to deploy the API
1. Run the tests.
2. Build the Docker image and push it to the registry.
3. Roll out with `kubectl apply -f deploy.yaml`.

## Rollback
If the deploy fails, roll back with `kubectl rollout undo`.
EOF

cat > work/meeting-2026-10-01.md <<'EOF'
# Weekly meeting
- Decided to move the deploy to Thursdays.
- [ ] Write the rollback runbook
EOF

cat > ideas/garden.md <<'EOF'
# Garden ideas
Plant tomatoes in spring. Water them every morning.
EOF
```

## 3. Add the folder as a collection

```sh
qmd collection add ~/qmd-demo --name demo --mask "**/*.md"
qmd collection list
qmd ls demo
```

## 4. Describe it with context

Context helps QMD and AI tools understand what each folder contains:

```sh
qmd context add qmd://demo "Demo notes"
qmd context add qmd://demo/work "Work notes: deploys and meetings"
```

## 5. Generate embeddings

This is needed for semantic search. The first run downloads small local models.

```sh
qmd embed
```

## 6. Search from the terminal

```sh
qmd search "deploy"                  # fast keyword search
qmd vsearch "how do I ship the API"  # semantic search
qmd query "rollback steps"           # hybrid + reranking (best quality)
qmd query --json "deploy"            # JSON output, handy for scripts
qmd get "demo/work/deploy/guide.md"  # print a document
```

## 7. Run the HTTP server

```sh
qmd mcp --http --daemon        # starts at http://localhost:8181
curl -s http://localhost:8181/health
```

Keyword search:

```sh
curl -s -X POST http://localhost:8181/query \
  -H 'Content-Type: application/json' \
  -d '{"searches":[{"type":"lex","query":"deploy"}],"limit":5}'
```

Keyword plus semantic search, with reranking:

```sh
curl -s -X POST http://localhost:8181/query \
  -H 'Content-Type: application/json' \
  -d '{"searches":[{"type":"lex","query":"deploy"},{"type":"vec","query":"how do I ship the API"}],"limit":5,"rerank":true}'
```

Stop it with `qmd mcp stop`.

## 8. Keep it up to date

When you add or edit notes:

```sh
qmd update   # re-index files
qmd embed    # embed new or changed content
```

Your setup lives in `~/.config/qmd/index.yml`, a plain YAML file you can read and edit.
