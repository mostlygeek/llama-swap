import type { AudioTranscriptionResponse } from "./types";
import { playgroundSessionHeaders } from "./playgroundSession";
import { apiUrl } from "./apiPath";

export async function transcribeAudio(
  model: string,
  file: File,
  signal?: AbortSignal
): Promise<AudioTranscriptionResponse> {
  const formData = new FormData();
  formData.append("file", file);
  formData.append("model", model);

  const response = await fetch(apiUrl("/v1/audio/transcriptions"), {
    method: "POST",
    headers: playgroundSessionHeaders,
    body: formData,
    signal,
  });

  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(`Audio API error: ${response.status} - ${errorText}`);
  }

  return response.json();
}
