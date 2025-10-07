# Improve macro-in-macro support

## Overview

- macros are currently unmarshalled into a `map[string]any`
  - map's do not have an order so macros that include macros can some cause substitution problems if the order happens to be wrong
  - if `B` includes `${A}` and `B` is substituted before `A`, `${A}` will not get replaced
  - see Bug Example below which illustrates this scenario
- what macro properties do we want?
  - macro replacement is single pass (prevents circulate dependencies)
  - macros substitutions are done in a LIFO (last in, first out) order
  - macros can only include macros that have been previously defined
  - macros are decoded into a data structure that maintains the order they were defined (`MapSlice`)
- there are three levels of macros (`global`, `model` , `reserved`)
- reserved macros: PORT and MODEL_ID
- the order macros should be substituded in `model`, `global` and `reserved`
- model level macros have precedence over global level, and can overwrite them before substitution (current behavior)
- macros can not refer to themselves (add a new guard and a test for this)
- the `gopkg.in/yaml.v3` package is now archived and no longer maintained.
  - Switch to using https://github.com/goccy/go-yaml
  - goccy/go-yaml supports `MapSlice` type
  - use a `MapSlice` which preserves order of macros in the order they were defined
- all current tests should continue to pass
- add new tests for the substitution order and changes

## Bug Example

```yaml
macros:
  "podman-llama": >
    podman run --name ${MODEL_ID}
    --init --rm -p ${PORT}:8080 -v /home/alex/ai/models:/models:z --gpus=all
    ghcr.io/ggml-org/llama.cpp:server-cuda

  "standard-options": >
    --no-mmap --jinja

  "kv8": >
    -fa on -ctk q8_0 -ctv q8_0
```

- during macro substitution if `${MODEL_ID}` comes _BEFORE_ `${podman-llama}`
- after the substitution `${MODEL_ID}` will continue to be in the configuration and an error will happen
