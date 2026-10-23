// The users area (internal/api/users.go): the workspace admin's list and add. Disable, password
// reset and role changes are the host's `batondeck-kanban user disable|passwd`, so nothing here.
import { req } from './http.js';

export interface LocalUser {
  id: string;
  login: string;
  displayName: string;
  role: 'admin' | 'member';
  disabled: boolean;
  createdAt: number;
}

export const listUsers = () => req<{ users: LocalUser[] }>('GET', '/api/users');

/** Adds a member; the answer carries the generated initial password, which no other call returns. */
export const addUser = (login: string, displayName: string) =>
  req<{ user: LocalUser; password: string }>('POST', '/api/users', displayName ? { login, displayName } : { login });
