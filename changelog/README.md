# changelog

`changelog` embeds llama-swap's release notes, one markdown file per
release, so other programs can display them. It returns the raw
markdown; rendering is up to the consumer.

## Use

```go
import "github.com/mostlygeek/llama-swap/changelog"

versions := changelog.Versions() // ["v200", ..., "v271"]

latest, err := changelog.Latest() // "v271"

note, err := changelog.Get("v271") // raw markdown
```

Add the dependency by commit, since llama-swap's `v270` style tags are
not semantic versions:

```shell
go get github.com/mostlygeek/llama-swap@<commit>
```
