import { useEffect, useState } from 'react';

export interface Toast {
  id: number;
  msg: string;
  kind: 'error' | 'info';
}

let toasts: Toast[] = [];
let listeners: Array<(t: Toast[]) => void> = [];
let nextId = 1;

function emit(): void {
  for (const l of listeners) l(toasts);
}
function dismiss(id: number): void {
  toasts = toasts.filter((t) => t.id !== id);
  emit();
}

/** Push a transient toast (auto-dismisses). The global error surface for the app. */
export function pushToast(msg: string, kind: 'error' | 'info' = 'error'): void {
  const t: Toast = { id: nextId++, msg, kind };
  toasts = [...toasts, t];
  emit();
  setTimeout(() => dismiss(t.id), 5000);
}

/**
 * Fire an async action from an event handler, surfacing a failure as a toast.
 *
 * `void somePromise()` does NOT do this. Every write that goes through `useMutation` is covered by the
 * `MutationCache.onError` handler in `main.tsx`; the handful of hand-rolled async actions bypass it, so
 * `void action()` turned a failed request into a completely silent no-op — the user clicks, nothing
 * happens, and the only trace is an `Uncaught (in promise)` nobody is looking at. That hit primary
 * navigation (clicking a project card), "Mark all read", and Sign out.
 */
export function fire(p: Promise<unknown>): void {
  void p.catch((e: unknown) => pushToast(e instanceof Error ? e.message : String(e)));
}

/** Always-mounted host that renders the toast stack. */
export function ToastHost() {
  const [list, setList] = useState<Toast[]>(toasts);
  useEffect(() => {
    listeners.push(setList);
    return () => {
      listeners = listeners.filter((l) => l !== setList);
    };
  }, []);
  return (
    <div className="toast-host" role="status" aria-live="polite">
      {list.map((t) => (
        <div key={t.id} className={`toast ${t.kind}`} onClick={() => dismiss(t.id)}>
          {t.msg}
        </div>
      ))}
    </div>
  );
}
