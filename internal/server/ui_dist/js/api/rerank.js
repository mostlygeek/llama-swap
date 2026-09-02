// Rerank. Verbatim port of lib/rerankApi.ts.
export async function rerank(model, query, documents, signal) {
  const response = await fetch("/v1/rerank", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model, query, documents }),
    signal,
  });
  if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);
  return response.json();
}
