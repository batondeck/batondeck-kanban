/**
 * THE TICKET'S INFORMATION ARCHITECTURE, DEFINED ONCE.
 *
 * The page and the drawer are two viewports onto one record, and they had drifted: the page's tab
 * list ordered Context → Discussion, the drawer's nav ordered Discussion → Context, and the panel
 * bodies matched their own nav. Same five labels, two hand-maintained arrays, two answers to "what
 * does this ticket look like". Nothing could catch it — both were internally consistent.
 *
 * `SECTIONS` is the single source for BOTH NAV BARS. What they still differ in is presentation: the
 * page shows the sections as `Tabs` beside a 420px rail, the drawer as chips above a 520px scroll
 * body.
 *
 * IT IS NOT THE SOURCE OF PANEL ORDER, and this docblock claimed it was until a review checked.
 * The bodies are hand-written JSX in `TaskPage.tsx` and `TaskDrawer.tsx`; reordering two entries
 * here moves both nav bars while both bodies stay put, and nothing failed. What actually holds the
 * bodies together is `ticket-ia-parity.test.tsx`, which compares the two surfaces' rendered
 * `.record-title` sequences TO EACH OTHER — so a one-surface reorder is caught (proven), and a
 * change that moves both together is not. Driving the panels off `SECTIONS` would close that, and is
 * a refactor rather than a docblock fix; until then, do not read this constant as owning the bodies.
 */
import { useRef } from 'react';
import type { RefObject } from 'react';

export const SECTIONS = [
  { id: 'details', label: 'Details' },
  { id: 'context', label: 'Context' },
  { id: 'work', label: 'Work' },
  { id: 'files', label: 'Files' },
  { id: 'discussion', label: 'Discussion' },
  { id: 'activity', label: 'Activity' },
] as const;

export type SectionId = (typeof SECTIONS)[number]['id'];
export type SectionRefs = Record<SectionId, RefObject<HTMLDivElement | null>>;

/**
 * One ref per section, plus the scroll helper both nav bars use.
 *
 * SIX HARDCODED `useRef` CALLS, deliberately — `SECTIONS.map(() => useRef(null))` is a hook inside a
 * callback and fails `react-hooks/rules-of-hooks` at the first step of the gate. Hardcoding also
 * keeps the ref identities stable, which matters here: the lease countdown re-renders these surfaces
 * once a second, and a freshly-created callback ref would detach and reattach on every tick.
 */
export function useSectionNav(): { refs: SectionRefs; scrollTo: (id: SectionId) => void } {
  const refs: SectionRefs = {
    details: useRef<HTMLDivElement>(null),
    context: useRef<HTMLDivElement>(null),
    work: useRef<HTMLDivElement>(null),
    files: useRef<HTMLDivElement>(null),
    discussion: useRef<HTMLDivElement>(null),
    activity: useRef<HTMLDivElement>(null),
  };
  const scrollTo = (id: SectionId): void => {
    refs[id].current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  };
  return { refs, scrollTo };
}
