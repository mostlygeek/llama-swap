// TTS generation. Verbatim port of lib/speechApi.ts.
export async function generateSpeech(model, input, voice, signal) {
  const response = await fetch("/v1/audio/speech", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model, input, voice }),
    signal,
  });
  if (!response.ok) {
    const errText = await response.text();
    throw new Error(`Speech API error: ${response.status} - ${errText}`);
  }
  return response.blob();
}
