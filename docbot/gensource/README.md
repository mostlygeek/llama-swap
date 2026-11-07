# gensource

Writes llama-swap's Go and TypeScript source, without tests, to a gzipped tar
for the `docbot` package's `code__*` tools. It archives the llama-swap version
the current module's `go.mod` selects, or a directory given with `-dir`.

```shell
go get -tool github.com/mostlygeek/llama-swap/docbot/gensource
go tool gensource -o llama-swap-src.tar.gz
```

See [../README.md](../README.md) for how to embed and serve the archive.
