const SESSION_ALPHABET = "abcdefghijklmnopqrstuvwxyz0123456789";
const SESSION_SUFFIX_LENGTH = 5;

function createPlaygroundSessionID(): string {
  const random = crypto.getRandomValues(new Uint8Array(SESSION_SUFFIX_LENGTH));
  let suffix = "";
  for (const value of random) {
    suffix += SESSION_ALPHABET[value % SESSION_ALPHABET.length];
  }
  return `lspg-${suffix}`;
}

// Module initialization runs once per UI load, so every Playground request
// made by this page shares one session identifier.
export const playgroundSessionID = createPlaygroundSessionID();

export const playgroundSessionHeaders = {
  "X-Session-ID": playgroundSessionID,
} as const;
