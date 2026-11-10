/**
 * The keyboard layer (Phase 2). One registry is the single source of truth: screens register scoped
 * bindings via useShortcuts(); the dispatcher fires the deepest active scope first; the `?` overlay
 * renders whatever is currently registered. Keys never fire while typing in an input/textarea/select
 * or a contenteditable, nor for Enter/Space on a focused button, link or ARIA widget (its own press).
 */
import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
// NOTE: the registry is deliberately IDENTITY-STABLE — registering/unregistering bindings causes no
// re-renders at all (a version-state here once created a provider-wide update storm that wedged React).

export interface Binding {
  /** Display chord, e.g. "j", "⌘K", "g b", "?" */
  keys: string;
  label: string;
  /** Return false to let the event fall through. */
  run: (e: KeyboardEvent) => void | boolean;
}
type Scope = 'global' | 'board' | 'drawer';
const SCOPE_ORDER: Scope[] = ['drawer', 'board', 'global']; // deepest first

interface Registry {
  register(scope: Scope, bindings: Binding[]): () => void;
  /** snapshot for the help overlay (read fresh when it opens) */
  all(): Array<{ scope: Scope; bindings: Binding[] }>;
}

const Ctx = createContext<Registry | null>(null);

function isTyping(e: KeyboardEvent): boolean {
  const t = e.target as HTMLElement | null;
  if (!t) return false;
  return (
    t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable
  );
}

/**
 * Is this a key the focused control acts on itself? Enter and Space activate a button, a link, a
 * summary or an ARIA widget, and arrows move inside a menu, listbox, tab list and the like. The
 * dispatcher listens on `window`, so without this the board's `Enter` ("open the selected task") took
 * Enter from every focused button and link while a card was selected, and its `preventDefault`
 * cancelled the control's own press. A chord with a modifier (⌘K) is never the control's.
 */
const ACTIVATES = 'button, a[href], summary, [role="button"], [role="link"], [role="menuitem"], [role="option"], [role="switch"], [role="checkbox"], [role="radio"], [role="tab"]';
const ARROW_WIDGETS = '[role="menu"], [role="menubar"], [role="listbox"], [role="tablist"], [role="radiogroup"], [role="slider"], [role="tree"], [role="grid"]';
function controlOwns(e: KeyboardEvent): boolean {
  if (e.metaKey || e.ctrlKey || e.altKey) return false;
  const t = e.target as Element | null;
  if (!t || typeof t.closest !== 'function') return false;
  if (e.key === 'Enter' || e.key === ' ') return !!t.closest(ACTIVATES);
  if (e.key.startsWith('Arrow')) return !!t.closest(ARROW_WIDGETS);
  return false;
}

/** Normalize an event to a chord string the registry matches on ("k", "⌘k", "shift+/" → "?"). */
function chordOf(e: KeyboardEvent): string {
  if (e.metaKey || e.ctrlKey) return `⌘${e.key.toLowerCase()}`;
  if (e.key === '?') return '?';
  return e.key.length === 1 ? e.key.toLowerCase() : e.key; // "Enter", "Escape", "ArrowDown", …
}

export function ShortcutProvider({ children }: { children: ReactNode }) {
  const scopes = useRef(new Map<Scope, Set<Binding[]>>());
  const pendingG = useRef(false); // g-chord state (Linear-style "g then x" navigation)

  const registry = useMemo<Registry>(
    () => ({
      register(scope, bindings) {
        if (!scopes.current.has(scope)) scopes.current.set(scope, new Set());
        scopes.current.get(scope)!.add(bindings);
        return () => {
          scopes.current.get(scope)?.delete(bindings);
        };
      },
      all() {
        return SCOPE_ORDER.filter((s) => scopes.current.get(s)?.size).map((s) => ({
          scope: s,
          bindings: [...(scopes.current.get(s) ?? [])].flat(),
        }));
      },
    }),
    [],
  );

  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      // Typing, a key the focused control acts on, or one a component has already handled: not ours.
      if (isTyping(e) || controlOwns(e) || e.defaultPrevented) return;
      const chord = chordOf(e);

      // g-chords: "g" arms a 1.2s window; the next key resolves "g x".
      if (pendingG.current) {
        pendingG.current = false;
        const gChord = `g ${chord}`;
        for (const scope of SCOPE_ORDER) {
          for (const set of scopes.current.get(scope) ?? []) {
            for (const b of set) {
              if (b.keys === gChord) {
                if (b.run(e) !== false) {
                  e.preventDefault();
                  return;
                }
              }
            }
          }
        }
        return; // unknown g-chord: swallow quietly
      }
      if (chord === 'g' && !e.metaKey && !e.ctrlKey) {
        pendingG.current = true;
        window.setTimeout(() => {
          pendingG.current = false;
        }, 1200);
        return;
      }

      for (const scope of SCOPE_ORDER) {
        for (const set of scopes.current.get(scope) ?? []) {
          for (const b of set) {
            if (b.keys === chord) {
              if (b.run(e) !== false) {
                e.preventDefault();
                return;
              }
            }
          }
        }
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  return <Ctx.Provider value={registry}>{children}</Ctx.Provider>;
}

/** Register bindings for a scope while the component is mounted. */
export function useShortcuts(scope: Scope, bindings: Binding[], deps: unknown[] = []): void {
  const reg = useContext(Ctx);
  useEffect(() => {
    if (!reg) return;
    return reg.register(scope, bindings);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reg, scope, ...deps]);
}

/** The `?` help overlay — renders straight from the registry, so it's never stale. */
export function ShortcutHelp() {
  const reg = useContext(Ctx);
  const [open, setOpen] = useState(false);
  useShortcuts('global', [
    { keys: '?', label: 'Keyboard shortcuts', run: () => setOpen((o) => !o) },
    { keys: 'Escape', label: '', run: () => (open ? setOpen(false) : false) },
  ], [open]);
  if (!open || !reg) return null;
  const groups = reg.all();
  return (
    <div className="help-overlay" onClick={() => setOpen(false)}>
      <div className="help-card" role="dialog" aria-label="Keyboard shortcuts" onClick={(e) => e.stopPropagation()}>
        <h2>Keyboard shortcuts</h2>
        {groups.map((g) => (
          <div key={g.scope} style={{ marginBottom: 14 }}>
            <div className="mono" style={{ fontSize: 10, letterSpacing: '0.08em', textTransform: 'uppercase', color: 'var(--text-muted)', margin: '0 0 4px' }}>
              {g.scope}
            </div>
            {g.bindings.filter((b) => b.label).map((b) => (
              <div className="help-row" key={`${g.scope}-${b.keys}-${b.label}`}>
                <span>{b.label}</span>
                <span className="kbd">{b.keys}</span>
              </div>
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}
