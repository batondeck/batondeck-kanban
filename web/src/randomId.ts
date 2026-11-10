/**
 * Random hex from the browser's CSPRNG, on every origin.
 *
 * `crypto.randomUUID` exists only in a secure context (HTTPS, or localhost), so on a plain-HTTP LAN
 * address it is undefined and every caller threw. `crypto.getRandomValues` exists in every context and
 * is the same generator, so ids and keys are minted from it instead.
 */
export function randomHex(bytes: number): string {
  const buf = new Uint8Array(bytes);
  crypto.getRandomValues(buf);
  return Array.from(buf, (b) => b.toString(16).padStart(2, '0')).join('');
}
