---
title: Realtime voice chat with /v1/realtime
summary: Proxy OpenAI Realtime API websockets to llama-server for speech to speech models like LFM3 Audio, and talk to them from the Playground.
category: guides
tags: [realtime, websocket, transcription, session.update, voice, speech, audio, lfm, liquid, llama-server, playground, microphone, https, secure context]
config_keys: [models.*.cmd, models.*.ttl, models.*.compat.ignoreWebsockets, models.*.concurrencyLimit, globalConcurrencyLimit]
updated: 2026-11-06
---

# Realtime voice chat with /v1/realtime

`/v1/realtime?model=<id>` is a websocket in the style of OpenAI's Realtime
API. llama-swap reads the model from the query, starts or swaps it like any
other request, then passes the websocket through untouched. llama-server
serves it for audio models such as
[LFM3-Audio-2B](https://huggingface.co/LiquidAI/LFM3-Audio-2B), which hear
speech and answer with text and speech together.

## Model in session.update instead of the URL

OpenAI's realtime transcription clients connect to
`/v1/realtime?intent=transcription` with no model in the URL and name it in
their first `session.update` event. llama-swap supports this: when the URL
has no `model`, it accepts the websocket itself, waits for a
`session.update` naming a model, and then starts or swaps that model and
connects the session to it. The model is read from, in order:

- `session.model`
- `session.audio.input.transcription.model`
- `session.input_audio_transcription.model` (beta
  `transcription_session.update`)

The events received while waiting are passed on to the model unchanged. The
rest of the URL query, such as `intent=transcription`, is kept and `model`
is added to it. Since the websocket is already open, a failure (no model
named, an unknown model, one that fails to start) is sent as an OpenAI style
`error` event followed by a close frame, rather than an HTTP error. The
client has 30 seconds to send the `session.update`.

The model is fixed for the life of the connection, whether it came from
`?model=` or the first `session.update` naming one. A later `session.update`
with a different model does not swap models: it is passed to the running
model unchanged. To use another model, open a new connection.

## Setup

```yaml
models:
  liquidfm-2B:
    cmd: >
      llama-server --host 127.0.0.1 --port ${PORT}
      -ngl 999
      --model /models/LFM3-Audio-2B-F16.gguf
      --mmproj /models/mmproj-LFM3-Audio-2B-F16.gguf
      --repeat-penalty 1.1 --repeat-last-n 2048
    ttl: 600
```

The `--mmproj` file is required: it carries both the audio encoder and the
speech generator. Use a llama-server build with `/v1/realtime` (b12063 was
used for this guide).

Sampling comes from two places:

- Audio sampling (`temp 1.0`, `top_k 128`, repeat penalty `1.1` over `4096`)
  is stored in the mmproj file, as the model card recommends. Nothing to set.
- Text decoding defaults to greedy with no repeat penalty, which can loop on
  one phrase. `--repeat-penalty 1.1 --repeat-last-n 2048` fixes that.

`filters.setParams` cannot set either: filters rewrite POST bodies, and a
realtime session sends its settings later, as websocket messages.

## Talking to it

Open the Playground's **Realtime** tab, pick the model and click Connect.
Push to talk (hold the button or the space bar) or hands-free, where a pause
ends your turn; its sensitivity and timings are in the tab's settings.

Any Realtime API client works too. What llama-server expects:

- Audio in is PCM16 mono at 16 kHz, out is 24 kHz; `session.created` states
  both. Send `input_audio_buffer.append`, then `input_audio_buffer.commit`
  and `response.create`. There is no server side turn detection, so the
  client decides when a turn ends.
- Ask for `output_modalities: ["text", "audio"]`. The model writes the text
  and speaks it.
- Start the instructions with `Respond with interleaved text and audio.`,
  the model's prompt for spoken chat. The Playground's default does.

## Microphone over plain http

The Playground connects with `ws://` or `wss://` to match the page, but the
browser only gives it the microphone in a secure context: an https page, or
one opened as `localhost`, `127.0.0.1` or `*.localhost`. Over
`http://<host>:8080` the mic is missing. Ways around it:

- **SSH tunnel:** `ssh -L 8080:localhost:8080 <host>`, then open
  `http://localhost:8080`. Nothing else to set up.
- **Chrome or Edge:** in `chrome://flags/#unsafely-treat-insecure-origin-as-secure`
  add `http://<host>:8080`, enable it and relaunch.
- **Firefox:** in `about:config` set `media.devices.insecure.enabled` and
  `media.getusermedia.insecure.enabled` to `true`.
- **https:** give llama-swap a certificate the browser trusts, from
  [mkcert](https://github.com/FiloSottile/mkcert), `tailscale cert`, or a
  reverse proxy such as Caddy in front of it. Safari only works this way.

The browser flags are per browser profile and lower its guard for that
origin, so keep them to hosts you trust.

## Swapping and ttl

An open session counts as an in-flight request for its whole life. The model
stays loaded and busy, so a swap that needs its memory waits until the session
closes, and `ttl` counts from when it closes. Disconnect when done. Unloading
the model by hand does not wait: it stops the model and ends the session.

The session also holds one slot of `models.*.concurrencyLimit` and of
`globalConcurrencyLimit` until it closes, idle or not. With a small global
limit, a few open sessions can turn other requests away with HTTP 429.

Do not set `compat.ignoreWebsockets` on a realtime model: the websocket could
then not start a stopped model, and the model could be swapped out mid-call.

Each session is one Activity row, written when it closes. See
`guides/operations/observability-storage-and-activity`.

## What goes wrong

- **"microphone unavailable" in the Playground.** Browsers only allow the
  microphone on https or `localhost`; the websocket itself works over plain
  http. See "Microphone over plain http" below.
- **The first reply describes or repeats what you said.** The instructions
  lack `Respond with interleaved text and audio.`, so the model treats the
  audio as something to describe.
- **Audio-only output is a second of garbled sound.** The model needs to
  write text to steer its speech. Always ask for text and audio.
- **A reply repeats one phrase until cut off.** Add the `--repeat-penalty`
  flags above. `max_output_tokens` defaults to `"inf"`, so set it in
  `session.update` to bound a runaway reply.
- **The connection drops right away.** The upgrade was refused, for example
  an unknown model or one that failed to start. Browsers only report close
  code 1006; the Activity page and llama-swap's log show the error.
- **`error` event "no model: name it in the session.update event".** The
  URL has no `?model=` and the first `session.update` named none either, or
  none arrived within 30 seconds. A client that waits for `session.created`
  before sending `session.update` needs `?model=` in the URL.
- **Works directly, fails behind a reverse proxy.** The proxy must forward
  websocket upgrades: pass the `Upgrade` and `Connection` headers through
  (nginx: `proxy_http_version 1.1` and `proxy_set_header` for both). For a
  path prefix see `guides/connectivity/reverse-proxy-subpath`.

## Related

- `guides/model-runtime/capabilities-and-model-listings`: audio output is
  detected from llama-server's `/v1/models`
- `guides/upstreams/comfyui`: the opposite case, a websocket that must not
  hold a model
