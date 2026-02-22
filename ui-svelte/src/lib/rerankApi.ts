export interface RerankResult {
  index: number;
  relevance_score: number;
}

export interface RerankResponse {
  model: string;
  object: string;
  usage: { prompt_tokens: number; total_tokens: number };
  results: RerankResult[];
}

export async function rerank(
  model: string,
  query: string,
  documents: string[],
  signal: AbortSignal
): Promise<RerankResponse> {
  const response = await fetch("/v1/rerank", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model, query, documents }),
    signal,
  });
  if (!response.ok) throw new Error(`${response.status} ${response.statusText}`);
  return response.json();
}
