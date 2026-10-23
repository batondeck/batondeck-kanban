// Owner: R2.3 fills these bodies; R1.12 copied the signatures from api.ts.
import type { Notification } from '../types.js';
import { req } from './http.js';

// notifications (caller-scoped, cross-project)
export const getNotifications = () => req<{ notifications: Notification[]; unread: number; nextCursor?: string }>('GET', '/api/notifications');

export const markNotificationsRead = (opts: { ids?: string[]; all?: boolean }) =>
  req<{ updated: number }>('POST', '/api/notifications/read', opts);
