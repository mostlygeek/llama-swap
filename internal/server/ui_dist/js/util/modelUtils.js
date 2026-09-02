// Verbatim port of lib/modelUtils.ts.
export function groupModels(models) {
  const available = models.filter((m) => !m.unlisted);
  const local = available.filter((m) => !m.peerID);
  const peerModels = available.filter((m) => m.peerID);
  const peersByProvider = peerModels.reduce((acc, m) => {
    const k = m.peerID || "unknown";
    (acc[k] = acc[k] || []).push(m);
    return acc;
  }, {});
  return { local, peersByProvider };
}
