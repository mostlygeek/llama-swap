# llama-swap knowledge base

Short, focused articles that the Playground's Docs agent can search and read.
Every file here is indexed at build time and served over MCP at `/api/mcp`, so an
LLM running on your own hardware can answer questions about llama-swap using
real text instead of guesswork.

## Layout

```
internal/docagent/kb/
  guides/
    configuration/*.md
    model-runtime/*.md
    routing/*.md
    connectivity/*.md
    api-integration/*.md
    operations/*.md
  examples/*.md    concrete, copy-pasteable configurations
  tutorials/*.md   start-to-finish walkthroughs
```

A document's **id** is its path without the extension, e.g.
`guides/model-runtime/ttl-and-unloading`. Guide IDs include their topic
directory. Ids are stable — renaming a file changes its id and
breaks links from other articles.

## Frontmatter

Every article starts with a YAML frontmatter block:

```markdown
---
title: Automatic model unloading with ttl
summary: How ttl, globalTTL and unloadTimeout interact, and how to unload on demand.
category: guides
tags: [ttl, unload, vram, memory]
config_keys: [globalTTL, unloadTimeout, models.*.ttl]
updated: 2026-08-25
---

# Automatic model unloading with ttl
...
```

| field | required | notes |
| --- | --- | --- |
| `title` | yes | one line, sentence case |
| `summary` | yes | one sentence, under 200 characters — this is what the index listing shows |
| `category` | yes | must match the top-level directory: `guides`, `examples` or `tutorials` |
| `tags` | no | lowercase keywords, used for filtering and search ranking |
| `config_keys` | no | config keys the article explains; each must resolve in `config-schema.json` |
| `updated` | no | `YYYY-MM-DD` |

`TestKB_FrontmatterIsValid` in `internal/reference` enforces all of the above,
including that every `config_keys` entry is a real key. Run `make test-dev`
after adding an article.

## Writing guidelines

- **Keep it short.** These get read into a local model's context window, often
  4k–32k tokens. One screen of text beats three.
- **Don't restate `config.example.yaml`.** It is indexed too, as
  `reference/config/<section>`. Link to it and explain the parts that confuse
  people instead.
- **Show a working config.** A fenced `yaml` block that someone can paste is
  worth more than a paragraph describing one.
- **Say what goes wrong.** The failure modes are the most valuable content here
  and the hardest to find elsewhere.
