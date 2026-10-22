/**
 * THE TICKET'S CONTROL COLUMN — status, ownership, and every property.
 *
 * The reference (Jira) puts the whole control surface on the right and leaves the main column as
 * pure content. Ours had them interleaved: Actions sat between the digest and the review note,
 * halfway down a column a reader scrolls for the DESCRIPTION, while the rail held only Properties.
 *
 * What goes here is everything that is *state about the ticket* rather than *content of the ticket*:
 *
 *  - `TicketActions` — the status control and the claim/complete/block/hand-off buttons.
 *  - `LeasePanel` — who holds it, counting down. Directly mutated by the buttons above it.
 *  - `ReviewPanel` — who owns the review and how long it has waited. Same reason.
 *  - `PropertiesPanel` — priority, assignee, due, estimate, labels, capabilities, custom fields.
 *
 * ONLY PROPERTIES COLLAPSES. A ticking lease countdown or an unowned-review callout behind a shut
 * fold defeats the panel's whole purpose; a long list of fields is exactly what a reader wants out of
 * the way once they have read it.
 *
 * IT SHIPS OPEN ON THE PAGE AND SHUT IN THE DRAWER (`compactDetails`), and that is a measured
 * difference rather than a taste one. On the page the rail sits BESIDE the content, so an open fold
 * costs nothing. In the 520px drawer it sits ABOVE it — and on staging the rail filled the entire
 * first screen, pushing the Description (the reason you opened the ticket) below the fold. Same
 * component, same order; only the initial disclosure differs, because only the geometry differs.
 *
 * The page mounts this in `SideWide`'s rail (420px, collapsing under 1100px); the drawer mounts it at
 * the top of its scroll body, where at 520px it reads as a header block. One component, two
 * placements, no `variant` prop.
 */
import { LeasePanel, PropertiesPanel, ReviewPanel } from './panels.js';
import { TicketActions } from './TicketActions.js';
import type { LoadedTaskDetail } from './useTaskDetail.js';

export function TicketRail({ d, compactDetails }: { d: LoadedTaskDetail; compactDetails?: boolean }) {
  return (
    <>
      <TicketActions d={d} />
      {/* Both render null when they do not apply — an empty Lease box on every unclaimed ticket, or
          an empty Review box on everything not in REVIEW, trains people to ignore the rail. */}
      <LeasePanel d={d} />
      <ReviewPanel d={d} />
      <PropertiesPanel d={d} defaultOpen={!compactDetails} />
    </>
  );
}
