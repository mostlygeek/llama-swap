// Stable Diffusion WebUI API. Verbatim port of lib/sdApi.ts.
export async function generateSdImage(request, signal) {
  const response = await fetch("/sdapi/v1/txt2img", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
    signal,
  });
  if (!response.ok) {
    const errText = await response.text();
    throw new Error(`SDAPI error: ${response.status} - ${errText}`);
  }
  return response.json();
}

export async function fetchSdLoras(model, signal) {
  const response = await fetch(`/sdapi/v1/loras?model=${encodeURIComponent(model)}`, { signal });
  if (!response.ok) {
    const errText = await response.text();
    throw new Error(`SDAPI loras error: ${response.status} - ${errText}`);
  }
  return response.json();
}
