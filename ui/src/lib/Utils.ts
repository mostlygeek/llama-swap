export function processEvalTimes(text: string) {
  const lines = text.match(/^ *eval time.*$/gm) || [];

  let totalTokens = 0;
  let totalTime = 0;

  lines.forEach((line) => {
    const tokensMatch = line.match(/\/\s*(\d+)\s*tokens/);
    const timeMatch = line.match(/=\s*(\d+\.\d+)\s*ms/);

    if (tokensMatch) totalTokens += parseFloat(tokensMatch[1]);
    if (timeMatch) totalTime += parseFloat(timeMatch[1]);
  });

  const avgTokensPerSecond = totalTime > 0 ? totalTokens / (totalTime / 1000) : 0;

  return [lines.length, totalTokens, Math.round(avgTokensPerSecond * 100) / 100];
}
