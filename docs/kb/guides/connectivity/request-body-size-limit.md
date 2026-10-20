---
title: Request body size limit
summary: Raise maxRequestBodyMB when large audio uploads or base64 images fail with HTTP 413 Request Entity Too Large.
category: guides
tags: [413, request-entity-too-large, chunked, upload, body-size, audio, images, base64, limit]
config_keys: [maxRequestBodyMB]
updated: 2026-10-20
---

# Request body size limit

`maxRequestBodyMB` caps the size, in megabytes, of a request body sent to a
model endpoint such as `/v1/chat/completions`, `/v1/embeddings` or
`/v1/audio/transcriptions`. The default is `250`.

```yaml
maxRequestBodyMB: 1024
```

A request over the limit fails with HTTP 413 and the message
`request body exceeds the 250 MB limit` (showing your configured value). The
request never reaches the model and no model is loaded for it.

The limit counts the bytes actually received, so it also applies to chunked
uploads that send no `Content-Length` header. A request with a
`Content-Length` over the limit is rejected before any of the body is read.

## What goes wrong

- **Large uploads fail with 413.** Long audio files and requests with many
  base64-encoded images are the usual cause. Base64 makes an image about a
  third larger than the file on disk. Raise `maxRequestBodyMB`.
- **Memory use grows with the limit.** llama-swap reads request bodies into
  memory to find the `model` field and to apply filters, so one request can
  use several times its own size in RAM. Raising the limit lets each request
  use more memory, and concurrent large requests add up.
- **`0` or a negative value is rejected** when the config loads. There is no
  setting for "no limit".
- **A 413 from somewhere else.** A reverse proxy in front of llama-swap (nginx
  `client_max_body_size`, for example) or the upstream server can have its own,
  smaller limit. If the error message is not the one above, look there.
