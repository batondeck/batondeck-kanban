// Copying text to the clipboard from any origin the server is reached at.
import { pushToast } from './toast.js';

/**
 * Copy `text`; resolves true when it was copied, false when no way worked. Never throws.
 *
 * `navigator.clipboard` exists only in a secure context (HTTPS, or localhost), so a team reaching
 * `batondeck-kanban serve` at http://10.0.0.5:8080 has none, and where it exists a refused permission
 * rejects. Both fall back to selecting the text in an off-screen textarea and `execCommand('copy')`,
 * which browsers still honour in an insecure context during a click.
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Refused (no permission, or the document lost focus): the textarea copy may still work.
  }
  return textareaCopy(text);
}

function textareaCopy(text: string): boolean {
  if (typeof document === 'undefined' || !document.body || typeof document.execCommand !== 'function') return false;
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.setAttribute('readonly', '');
  ta.setAttribute('aria-hidden', 'true');
  ta.setAttribute('data-copy', '');
  Object.assign(ta.style, { position: 'fixed', top: '0', left: '0', opacity: '0', pointerEvents: 'none' });
  const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  document.body.appendChild(ta);
  ta.focus();
  ta.select();
  let ok = false;
  try {
    ok = document.execCommand('copy');
  } catch {
    ok = false;
  }
  ta.remove();
  previous?.focus();
  return ok;
}

/**
 * Copy a link from a click and say what happened: `done` as a toast when it was copied, else the link
 * in a prompt where it is selected, ready to copy by hand (or, with no prompt, in an error toast).
 */
export function copyLink(url: string, done: string): void {
  void copyText(url).then((ok) => {
    if (ok) {
      pushToast(done, 'info');
      return;
    }
    try {
      window.prompt('Copy this link:', url);
    } catch {
      pushToast(`Couldn’t copy the link: ${url}`);
    }
  });
}
