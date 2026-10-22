/* The theme attribute, and the only code allowed to write it.
 *
 * THREE STATES, NOT TWO. `system` is the default and is NOT the same as `light`: it means "follow the
 * OS", which the stylesheet implements with `@media (prefers-color-scheme: dark)`. A two-state toggle
 * would have to pick one on first load and would then ignore the OS forever — the behaviour every
 * user who has already set a system-wide preference reads as a bug.
 *
 * `system` is expressed by REMOVING the attribute, so the media query is what decides. `light` and
 * `dark` set it, and both stylesheet selectors are written to let an explicit choice win over the OS
 * in either direction (index.css, the DARK banner).
 *
 * WHY THIS EXISTS AT ALL, given T-398 deleted its predecessor on 2026-08-10: that machinery was
 * removed because only ONE palette shipped, so `applyTheme` could only ever delete an attribute
 * nothing set. Two complete, contrast-measured palettes ship here (T-519), so the mechanism can act.
 */
export type Theme = 'light' | 'dark' | 'system';

const KEY = 'bd.theme';

/** Never throws. Private-mode Safari and "block site data" make `localStorage` itself throw on
 *  ACCESS, not just on write — an uncaught read here would take the whole app down at boot. */
function read(): Theme {
  try {
    const v = localStorage.getItem(KEY);
    return v === 'light' || v === 'dark' ? v : 'system';
  } catch {
    return 'system';
  }
}

export function getTheme(): Theme {
  return read();
}

/** Write the attribute. `system` REMOVES it — see the note above; setting `data-theme="system"`
 *  would match neither stylesheet selector and silently pin the app to light. */
export function applyTheme(t: Theme): void {
  const root = document.documentElement;
  if (t === 'system') root.removeAttribute('data-theme');
  else root.setAttribute('data-theme', t);
}

export function setTheme(t: Theme): void {
  try {
    if (t === 'system') localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, t);
  } catch {
    /* Storage is unavailable — the choice still applies to THIS page, it just will not survive a
       reload. Failing the click outright would be worse than a preference that does not persist. */
  }
  applyTheme(t);
}

/** Call once, as early as possible — before React renders, or the first paint is the wrong theme. */
export function initTheme(): void {
  applyTheme(read());
}
