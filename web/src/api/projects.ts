// The projects area (internal/api/projects.go): projects, members, settings.
import type { Project } from '../types.js';
import { req, P } from './http.js';

// `members` / `memberDisplay` / `color` are what the Projects CARD renders — carried here so the grid
// does not fan out one `getProject` per project. NULL for a workspace admin listing projects they are
// not a member of (`all:true`), which is an authorization gate, not an oversight: treat null as "no
// detail available", never as "this project has no members".
export const listProjects = () =>
  req<{
    projects: Array<{
      projectId: string;
      name: string;
      role: string;
      members?: Record<string, string> | null;
      memberDisplay?: Record<string, string> | null;
      color?: string | null;
    }>;
  }>('GET', '/api/projects');

export const createProject = (name: string) => req<{ projectId: string }>('POST', '/api/projects', { name });

// `memberDisplay` is derived server-side per read (T-319), so it is a SIBLING of `project`, never a
// field on it. A member absent from the map has no better label than its id — render the id.
export const getProject = (projectId: string) =>
  req<{ project: Project; memberDisplay?: Record<string, string> }>('GET', P(projectId));

export const addMember = (projectId: string, identityId: string, role: string) =>
  req<{ members: Record<string, string> }>('POST', `${P(projectId)}/members`, { identityId, role });

export const removeMember = (projectId: string, identityId: string) =>
  req<{ ok: true }>('DELETE', `${P(projectId)}/members/${encodeURIComponent(identityId)}`);

export const updateSettings = (projectId: string, patch: Record<string, unknown>) =>
  req<{ settings: Project['settings'] }>('PATCH', `${P(projectId)}/settings`, patch);
