import type { SdApiTxt2ImgRequest, SdApiResponse, SdApiLora } from "./types";

export async function generateSdImage(
  request: SdApiTxt2ImgRequest,
  signal?: AbortSignal
): Promise<SdApiResponse> {
  const response = await fetch("/sdapi/v1/txt2img", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
    signal,
  });

  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(`SDAPI error: ${response.status} - ${errorText}`);
  }

  return response.json();
}

export async function fetchSdLoras(
  model: string,
  signal?: AbortSignal
): Promise<SdApiLora[]> {
  const response = await fetch(
    `/sdapi/v1/loras?model=${encodeURIComponent(model)}`,
    { signal }
  );

  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(`SDAPI loras error: ${response.status} - ${errorText}`);
  }

  return response.json();
}
