// OpenAI-compatible image generation. Verbatim port of lib/imageApi.ts.
export async function generateImage(model, prompt, size, signal) {
  const response = await fetch("/v1/images/generations", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model, prompt, n: 1, size }),
    signal,
  });
  if (!response.ok) {
    const errText = await response.text();
    throw new Error(`Image API error: ${response.status} - ${errText}`);
  }
  return response.json();
}
