# docbot

`docbot` serves llama-swap's documentation, and optionally its source code, as
MCP tools over HTTP, for programs other than llama-swap. It is the
documentation half of llama-swap's own `/api/mcp`: the same protocol and the
same `docs__*` and `code__*` tools, without the tools that need a running
llama-swap (`config__get_config`, `sys__now`).

The llama-swap.ai website uses it to run the Help agent in the browser.

## Use

```go
import "github.com/mostlygeek/llama-swap/docbot"

bot, err := docbot.New(docbot.Options{Name: "my-site"})
if err != nil {
	log.Fatal(err)
}
http.Handle("/api/mcp", bot)

// The Docs Agent system prompt, listing only the tools bot serves.
prompt := bot.SystemPrompt()
```

Add it with a commit, since llama-swap's `v270` style tags are not semantic
versions:

```shell
go get github.com/mostlygeek/llama-swap@<commit>
```

## Source code for the code__* tools

The documentation is embedded in the package. The source code is not:
`go:embed` cannot reach files outside the package directory. The `gensource`
command writes it to an archive at build time instead, from the llama-swap
version your `go.mod` selects, so the docs and the code always match.

```shell
go get -tool github.com/mostlygeek/llama-swap/docbot/gensource
```

```go
//go:generate go tool gensource -o llama-swap-src.tar.gz

//go:embed llama-swap-src.tar.gz
var srcArchive []byte

src, err := docbot.SourceFromArchive(srcArchive)
bot, err := docbot.New(docbot.Options{Source: src})
```

Run `go generate` again after updating llama-swap in `go.mod`. The archive is
about 500 KB and the same source always gives the same bytes, so it can be
committed. `gensource -dir ../llama-swap` archives a local checkout instead.

`code__read` links each file to GitHub at the llama-swap commit the binary was
built with. Set `Options.SourceRef` to link elsewhere.
