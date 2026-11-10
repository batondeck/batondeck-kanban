/** What Tab can reach inside the dialog, before the visibility check below. */
const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * A dialog opened over this one keeps its own Tab. The command palette is a plain overlay (cmdk sets
 * no dialog role), so it is named here too.
 */
const OVERLAID = '[role="dialog"], [role="alertdialog"], [aria-modal="true"], .palette-overlay';

/**
 * Whether Tab could land on el: not inside a `hidden` or `inert` subtree and not display:none. A
 * browser answers with checkVisibility, which also sees a stylesheet's display:none; jsdom has none,
 * so there an inline display:none (the hidden file input) is what is checked.
 */
function shown(el: HTMLElement): boolean {
  if (el.closest('[hidden], [inert]')) return false;
  if (typeof el.checkVisibility === 'function') return el.checkVisibility({ visibilityProperty: true });
  return !el.closest('[style*="display: none"]');
}

/**
 * Keep Tab inside the modal: past its last control to its first, Shift+Tab from its first (or from the
 * dialog itself, focused on open) to its last, and from outside it to its first. A dialog with nothing
 * to focus yet (the drawer while its ticket loads) keeps the focus on itself. Without this Tab walked
 * out into the board under the overlay, where the keyboard user could no longer see what was focused.
 */
export function trapTab(e: KeyboardEvent, root: HTMLElement | null): void {
  if (!root) return;
  const active = document.activeElement;
  if (!root.contains(active) && active && active !== document.body && active.closest(OVERLAID)) return;
  const items = [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(shown);
  if (items.length === 0) {
    e.preventDefault();
    root.focus();
    return;
  }
  const first = items[0]!;
  const last = items[items.length - 1]!;
  if (!root.contains(active)) {
    e.preventDefault();
    first.focus();
  } else if (e.shiftKey && (active === first || active === root)) {
    e.preventDefault();
    last.focus();
  } else if (!e.shiftKey && active === last) {
    e.preventDefault();
    first.focus();
  }
}
