// Runtime helpers for chat message content, ported from the getTextContent /
// getImageUrls helpers in lib/types.ts. Content is either a plain string or an
// array of { type: "text", text } / { type: "image_url", image_url: { url } } parts.

export function getTextContent(content) {
  if (typeof content === "string") {
    return content;
  }
  return content
    .filter((part) => part.type === "text")
    .map((part) => part.text)
    .join("\n");
}

export function getImageUrls(content) {
  if (typeof content === "string") {
    return [];
  }
  return content
    .filter((part) => part.type === "image_url")
    .map((part) => part.image_url.url);
}
