import type { ImageGenerationRequest, ImageGenerationResponse } from "./types";

export async function generateImage(
  model: string,
  prompt: string,
  size: string,
  signal?: AbortSignal
): Promise<ImageGenerationResponse> {
  const request: ImageGenerationRequest = {
    model,
    prompt,
    n: 1,
    size,
  };

  const response = await fetch("/v1/images/generations", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
    signal,
  });

  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(`Image API error: ${response.status} - ${errorText}`);
  }

  return response.json();
}
