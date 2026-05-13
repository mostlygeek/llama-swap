import type { AudioTranscriptionResponse } from "./types";
import { getAuthHeaders } from "../stores/auth";

export async function transcribeAudio(
  model: string,
  file: File,
  signal?: AbortSignal
): Promise<AudioTranscriptionResponse> {
  const formData = new FormData();
  formData.append("file", file);
  formData.append("model", model);

  const response = await fetch("/v1/audio/transcriptions", {
    method: "POST",
    headers: {
      ...getAuthHeaders(),
    },
    body: formData,
    signal,
  });

  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(`Audio API error: ${response.status} - ${errorText}`);
  }

  return response.json();
}
