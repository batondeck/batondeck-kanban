/**
 * Notifications center (T-33): the caller's per-user in-app feed (currently @mentions). Newest-first list
 * with read/unread, mark-all-read, and click-through that deep-links to the task on its board. The unread
 * count drives the nav badge (NotificationsNavLink below).
 */
import { useEffect } from 'react';
import { Icon } from '../icons.js';
import { useNavigate } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { NavLink } from 'react-router';
import * as notificationsApi from '../api/notifications.js';
import * as streamApi from '../api/stream.js';
import { Avatar, EmptyState, PageHeader, timeAgo } from '../components.js';
import { fire } from '../toast.js';
import type { Notification } from '../types.js';
import { Stack } from '../ui.js';

const NOTIF_KEY = ['notifications'];

function message(n: Notification): string {
  if (n.type === 'mention') return `${n.actor} mentioned you in a comment`;
  if (n.type === 'follow_up') return `${n.actor} sent you a follow-up`;
  if (n.type === 'review_request') return `${n.actor} asked you to review a ticket`;
  return `${n.actor}: ${n.type}`;
}

export function Notifications() {
  const qc = useQueryClient();
  const nav = useNavigate();
  // NO POLL: `NotificationsNavLink` (mounted app-wide) opens the identity-keyed /api/stream and writes
  // snapshots into NOTIF_KEY, which this query shares — so this list stays live without its own stream
  // or its own poll. The initial fetch stays so it renders before the first snapshot arrives.
  const q = useQuery({
    queryKey: NOTIF_KEY,
    queryFn: () => notificationsApi.getNotifications(),
  });
  const notifications = q.data?.notifications ?? [];

  const markRead = async (ids?: string[]): Promise<void> => {
    await notificationsApi.markNotificationsRead(ids ? { ids } : { all: true });
    await qc.invalidateQueries({ queryKey: NOTIF_KEY });
  };
  const open = (n: Notification): void => {
    if (!n.read) fire(markRead([n.id]));
    if (n.boardId && n.taskId)
      void nav(`/projects/${n.projectId}/boards/${n.boardId}?task=${n.taskId}`);
  };

  return (
    <>
      <PageHeader
        title="Notifications"
        actions={
          <button
            className="btn secondary"
            disabled={(q.data?.unread ?? 0) === 0}
            onClick={() => fire(markRead())}
          >
            Mark all read
          </button>
        }
      />
      <div className="page" style={{ maxWidth: 720, margin: '0 auto' }}>
      {notifications.length === 0 ? (
        <EmptyState icon={<Icon name="bell" size={22} />} title="You're all caught up." hint="Mentions, follow-ups, and handoffs land here." />
      ) : (
        <Stack gap={4}>
          {notifications.map((n) => (
            <button
              key={n.id}
              className="card"
              onClick={() => open(n)}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 10,
                padding: 12,
                textAlign: 'left',
                cursor: 'pointer',
                borderLeft: n.read ? '3px solid transparent' : '3px solid var(--accent)',
                // `--surface-2` is declared nowhere, so this row's blue tint was NOT a fallback —
                // it was the colour, on every unread notification, in both themes. A wash mixed
                // from --accent follows the palette instead.
                background: n.read ? undefined : 'color-mix(in srgb, var(--accent) 8%, transparent)',
              }}
            >
              <Avatar id={n.actor} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 14 }}>{message(n)}</div>
                <div className="muted" style={{ fontSize: 11 }}>
                  {n.taskId ? `${n.taskId} · ` : ''}
                  {timeAgo(n.ts)}
                </div>
              </div>
              {!n.read && (
                <span className="chip" style={{ fontSize: 10 }}>
                  new
                </span>
              )}
            </button>
          ))}
        </Stack>
      )}
      </div>
    </>
  );
}

/**
 * Nav item with a live unread badge.
 *
 * THIS is where the identity-keyed stream is opened, not the Notifications screen — this component is
 * mounted app-wide, so the badge stays live on every screen, and the screen shares NOTIF_KEY so it
 * updates from the same snapshots for free. Opening it on the screen instead would mean the badge only
 * updated while the user was already looking at the notifications page.
 */
export function NotificationsNavLink() {
  const qc = useQueryClient();
  // NO POLL: replaced by `event: notifications` snapshots on /api/stream (Tier 2). The initial fetch
  // stays so the badge renders before the first snapshot arrives.
  const q = useQuery({
    queryKey: NOTIF_KEY,
    queryFn: () => notificationsApi.getNotifications(),
  });
  useEffect(
    () => streamApi.userStream({ onNotifications: (d) => qc.setQueryData(NOTIF_KEY, d) }),
    [qc],
  );
  const unread = q.data?.unread ?? 0;
  return (
    <NavLink
      to="/notifications"
      style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}
    >
      {/* Icon inlined (not from App's ICONS) to avoid a circular import; matches NavIcon's markup. */}
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
        <svg
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.8"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9M13.73 21a2 2 0 0 1-3.46 0" />
        </svg>
        Notifications
      </span>
      {unread > 0 && (
        /* `.nav-badge`, not inline: white on `var(--accent)` measured ~2.4:1 once the dark palette
           landed, and an inline pair is invisible to contrast.test.ts, which only reads tokens.
           `--ink`/`--ink-fg` is the one pair the guard proves in BOTH themes, and the class carries
           the inversion the filled active row needs. */
        <span className="nav-badge">{unread > 99 ? '99+' : unread}</span>
      )}
    </NavLink>
  );
}
