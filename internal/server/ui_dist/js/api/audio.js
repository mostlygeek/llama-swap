// Audio transcription. Verbatim port of lib/audioApi.ts.
export async function transcribeAudio(model, file, signal) {
  const fd = new FormData();
  fd.append("file", file);
  fd.append("model", model);
  const response = await fetch("/v1/audio/transcriptions", { method: "POST", body: fd, signal });
  if (!response.ok) {
    const errText = await response.text();
    throw new Error(`Audio API error: ${response.status} - ${errText}`);
  }
  return response.json();
}
