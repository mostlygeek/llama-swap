import type { SpeechGenerationRequest } from "./types";

export async function generateSpeech(
  model: string,
  input: string,
  voice: string,
  signal?: AbortSignal
): Promise<Blob> {
  const request: SpeechGenerationRequest = {
    model,
    input,
    voice,
  };

  const response = await fetch("/v1/audio/speech", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
    signal,
  });

  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(`Speech API error: ${response.status} - ${errorText}`);
  }

  return response.blob();
}
