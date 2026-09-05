// yamlQuote renders a value as a double-quoted YAML scalar. JSON's escaping
// rules (backslash, quote, control characters) are a subset of YAML's
// double-quoted style, so this keeps a model ID a plain string even when it
// contains YAML-special characters (e.g. "foo: bar", "#foo", "*anchor").
export function yamlQuote(value: string): string {
  return JSON.stringify(value);
}

// peerConfigYaml builds a ready-to-paste `peers` entry for a friend to add to
// their own config. Requires a non-empty models list; PeerConfig.Models
// rejects an empty list, so callers must not invoke this when
// $tailcatStatus.models is empty.
export function peerConfigYaml(address: string, models: string[]): string {
  const modelLines = models.map((model) => `      - ${yamlQuote(model)}`).join("\n");
  // tailcatKey is commented out: without it, connecting uses an ephemeral
  // client identity, so nobody's private key ends up in a shared snippet.
  // The commented line shows how to generate a stable one if this server
  // allowlists callers.
  return `peers:\n  friend:\n    proxy: tailcat://${address}\n    # generate with: tailcat genkey --client --key=/path/to/client.private.json\n    # tailcatKey: /path/to/client.private.json\n    models:\n${modelLines}\n`;
}
