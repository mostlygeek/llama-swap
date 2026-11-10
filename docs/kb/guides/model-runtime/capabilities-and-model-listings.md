---
title: Model capabilities and model listings
summary: Advertise images, tools and context length in /v1/models, automatically or by hand.
category: guides
tags: [capabilities, voices, speech, models, tools, vision, context, autodetect, embeddings, reranker, llama-server, vllm, halogen, playground, decisions]
config_keys: [models.*.capabilities, models.*.capabilities.disableAuto, models.*.capabilities.voices, models.*.capabilities.embeddings, models.*.capabilities.reranker, models.*.metadata, store.path]
updated: 2026-11-10
---

# Model capabilities and model listings

`capabilities` describes what a model can accept or produce. llama-swap reports
it through `/v1/models` so clients can pick a compatible model. This is listing
metadata only: it does not change routing or how requests are proxied. In
particular `tools: true` makes the listing report
`capabilities.function_calling: true` and advertise `tools` and `tool_choice`
as supported parameters. It does not enable tool calling by itself; the model
still needs a compatible chat template and runtime.

## Automatic discovery

llama-swap asks the upstream server what it supports the first time a model
becomes ready, and caches the answer. Anything you do not set yourself is
filled in from that answer.

```yaml
models:
  vision-model:
    cmd: llama-server -m vision.gguf --mmproj mmproj.gguf --port ${PORT}
    # no capabilities block needed
```

After this model has been loaded once, `/v1/models` reports its context length
and image input on its own.

The context length reported is the window one request can actually use, not
the one the model was trained for. A llama-server started with a smaller
`--ctx-size` than the model supports advertises the smaller number, and a
server whose slots are capped advertises the per-slot window rather than the
total it loaded.

What each server can report:

| | llama-server | vLLM | halogen |
| --- | --- | --- | --- |
| context length | yes, the loaded `n_ctx` | yes, `max_model_len` | yes |
| image input | yes | no | yes |
| audio or video input | yes | no | no |
| tools | yes, from the chat template | no | yes |
| decision model output | yes, from `/v1/models` | no | no |
| audio output (speech) | yes, from `/v1/models` | no | no |
| reranker | no | no | no |
| embeddings | no | no | no |

llama-server is read from `/props`, and halogen-flash-server from its
`/health`, which that project documents as the authoritative report of what
the running build accepts. Both vary per deployment: halogen serves images
only when it was started with a vision tower, and that is read rather than
assumed.

What a model outputs is the one thing `/props` cannot show, so it is read
from the `output_modalities` in llama-server's own `/v1/models`. Decision
models answer `/v1/systemone` and generate no text; llama-server marks them
with `["decisions"]`, and llama-swap reports `out: [decisions]` and no tools
for them, whatever their chat template says. The listing also flags them
with `capabilities.decisions: true`, which the UI shows as a Decisions
badge. Speech models report audio:
LFM Audio reports `["text", "audio"]` and gets `out: [text, audio]`, and a
speech-only model reports `["audio"]` and gets `out: [audio]`. Audio output
alone does not mean a model can hold a `/v1/realtime` conversation (the
Playground's Realtime tab); that also needs a model that chats, such as LFM
Audio.

vLLM is the thin one. Nothing it serves says whether it was started with
`--enable-auto-tool-choice`, or whether the model takes images, so set those
by hand on vLLM models. Other servers whose `/v1/models` lists a context
length, such as Gufo and yamz (`max_context`), are read the same way: context
length only.

llama-swap picks the right reader from the `owned_by` field in the server's
own `/v1/models`: `llamacpp`, `vllm` or `halogen`. To check what a server
will be detected as, ask it directly:

```bash
curl -s localhost:PORT/v1/models | jq -r '.data[0].owned_by'
```

Servers reporting anything else are left alone, including image, speech and
transcription servers, which have no capability surface to read. A llama.cpp
fork that keeps the upstream API reports `llamacpp` and is read the same way.

## Setting capabilities by hand

Values you write win, field by field, as long as they are not the zero value
for their type. Discovery only fills the gaps, so you can correct one field
without giving up the rest:

```yaml
models:
  vllm-model:
    cmd: vllm serve my-model --port ${PORT} --enable-auto-tool-choice
    capabilities:
      # vLLM cannot report this, so say it here
      tools: true
      # context is still filled in automatically from max_model_len
```

Set only what the model really supports. A false claim makes clients send
requests that fail or get ignored.

## Marking embedding and rerank models

Embedding servers have nothing to probe: no endpoint tells an embedding
model from a chat model without sending a real request, so discovery
never fills this in. The same is true for a reranker. Set the flag by
hand on both:

```yaml
models:
  gte-modernbert:
    cmd: llama-server -m gte-modernbert-F16.gguf --embedding --pooling cls --port ${PORT}
    capabilities:
      embeddings: true
      context: 8192
  gte-reranker:
    cmd: llama-server -m gte-reranker-F16.gguf --embedding --pooling rank --port ${PORT}
    capabilities:
      reranker: true
      context: 8192
```

The flags only change listing metadata: `/v1/models` reports
`capabilities.embeddings: true` or `capabilities.reranker: true` so
clients can tell these models apart from chat models, filter for them,
and show a badge. Neither flag starts, stops or reroutes anything; what
the server actually serves is decided by its command line. Without the
flag an embedding model that reports `text -> text` from `/props` is
indistinguishable from a chat model in the listing.

## Reading /props without loading the model

`GET /props?model=<id>` is forwarded to the model, so by default it starts
the model, and under a tight `capacity` pool it stops another model to make
room. A client that reads the context size of every model in the list, such
as the llama.vscode extension, would load them one after another.

Add `autoload=false`, the same parameter llama-server's router mode takes, to
read `/props` only from a model that is already running:

```bash
curl -i "localhost:8080/props?model=my-model&autoload=false"
```

A local model that is ready or still starting answers as usual. A local model
in any other state is not started and nothing is stopped; the request gets
`409 Conflict`. The client can then use `context_length` from `/v1/models`,
which is filled in after the model has been loaded once.

Only `autoload=false` and other values that are not `true` or `1` skip loading,
so `autoload=0` does too. Leaving the parameter out, or empty, loads the model
as before. Only `/props` checks it, and only for local models. A request for a
peer model is forwarded unchanged, and the peer decides what to do with it.

## Turning discovery off

A field set to its zero value (`false`, `0`, `[]`) cannot be told apart from
one you never wrote, so `tools: false` will not override a discovered `true`.
To take full control of a model's listing, turn discovery off:

```yaml
models:
  my-model:
    cmd: llama-server -m model.gguf --port ${PORT}
    capabilities:
      disableAuto: true
      in: [text]
      out: [text]
      context: 8192
```

With `disableAuto: true` the model advertises exactly what you wrote, and
nothing else. On its own, with no other fields, it advertises nothing.

## Setting the voice list for a speech model

`GET /v1/audio/voices?model={id}` normally goes to the upstream server.
`capabilities.voices` is an override: set it to answer the request from the
config instead:

```yaml
models:
  tts:
    cmd: my-tts-server --port ${PORT}
    capabilities:
      in: [text]
      out: [audio]
      voices: ["voicea", "voiceb", "voicec"]
```

```bash
curl -s 'localhost:8080/v1/audio/voices?model=tts'
# {"model":"tts","voices":["voicea","voiceb","voicec"]}
```

The response is a JSON object with a `voices` array and the `model` id. OpenAI
has no endpoint for listing voices, so this is a llama-swap convention, not an
OpenAI format. The Playground's speech tab reads it. The model is not started to
answer, and voices are not shown in `/v1/models`.
When `voices` is empty or unset there is no override, and the request goes to
the upstream server as before, which starts the model if it is not running.
This applies to local models only, not peers.

## Custom metadata in model listings

A model's `metadata` block is copied into `/v1/models` as written, under
`meta.llamaswap`. It is separate from the fields generated from
capabilities, which sit at the top level of the model entry, so the two never
overwrite each other:

```yaml
models:
  example:
    cmd: llama-server -m model.gguf --port ${PORT}
    metadata:
      context_window: 59940
      reasoning: true
```

Once the model has been loaded and its capabilities discovered, the listing
looks like this:

```json
{
  "id": "example",
  "context_length": 60160,
  "context_window": 60160,
  "meta": {
    "llamaswap": { "context_window": 59940, "reasoning": true },
    "n_ctx": 60160
  }
}
```

Top-level `context_window` is the discovered value and
`meta.llamaswap.context_window` is yours. Clients that read your own key must
look under `meta.llamaswap`. Metadata is the same before and after discovery.

llama-swap adds a few keys of its own to `meta.llamaswap` (`type`, `aliases`,
`upstream`, and `modelID` on alias entries). If your `metadata` uses one of
those names, llama-swap's value wins.

## How the Playground uses this

Each Playground tab looks for the modalities it needs and lists those models
first, under "Matching Capabilities":

| tab | wants |
| --- | --- |
| Chat | `in` has `text` or `image`, `out` has `text` |
| Images | `out` has `image` |
| Speech | `in` has `text`, `out` has `audio` |
| Transcription | `in` has `audio`, `out` has `text` |
| Rerank | `reranker: true` |
| Help's Docs agent | `tools: true` |

Discovery fills most of this in on its own once a model has been loaded, so a
llama-server model usually sorts itself. Models that report nothing are still
listed, further down under "Local", so nothing disappears from the selector for
want of configuration. Image, speech and transcription servers are the ones
worth setting by hand: nothing is read from them, so without an `in`/`out` block
they never sort to the top of their own tab.

## What goes wrong

**A model that has never been loaded shows nothing.** The server has to be
running to be asked. Load the model once and the values appear, including
after it unloads again.

**Discovered values do not survive a restart unless you configure a store.**
The cache lives in llama-swap's SQLite database, which is in memory unless
`store.path` is set:

```yaml
store:
  path: /var/lib/llama-swap/llama-swap.db
```

Without it, every restart starts from an empty cache and each model has to be
loaded again before its capabilities reappear.

**The cache can be stale.** Values are refreshed each time a model starts, so
between loads they describe the last run. Changing a model's `cmd` invalidates
its entry immediately, so shrinking `--ctx-size` will not keep advertising the
old number, but other changes to the same command (a different `--mmproj`, for
example) only take effect the next time the model loads.

**Tool support is read from the chat template.** llama-server reports what the
template can do, not what the model was trained for. A template that renders a
tool list is advertised as supporting tools even if the model is bad at using
them.
