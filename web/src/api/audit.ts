// The audit area (internal/api/audit.go): list_audit_log over GET, the workspace admin's alone.
import { req } from './http.js';

/** One audit row, every field a string ('' where it records nothing); `data` is a JSON object as text. */
export interface AuditEntry {
  ts: string;
  action: string;
  actor: string;
  agent: string;
  subject: string;
  projectId: string;
  result: string;
  errorCode: string;
  data: string;
}

export interface AuditFilter {
  actor?: string;
  subject?: string;
  action?: string;
  days?: number;
  limit?: number;
}

export const listAuditLog = (f: AuditFilter = {}) => {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) if (v !== undefined && v !== '') q.set(k, String(v));
  const qs = q.toString();
  return req<{ entries: AuditEntry[]; days: number }>('GET', `/api/audit${qs ? `?${qs}` : ''}`);
};
