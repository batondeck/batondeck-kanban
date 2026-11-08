/**
 * Global project selection. Fetches the roster via the same ['projects'] react-query key the screens
 * use (shared cache, no extra request), persists the pick to localStorage, and validates it against
 * the list (falls back to the first project). No visual output — the sidebar switcher renders it.
 */
import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import * as projectsApi from './api/projects.js';

export interface ProjectInfo {
  projectId: string;
  name: string;
  role: string;
  /** `list_projects` names the project's members on every row (account id → display name, else login). */
  memberDisplay?: Record<string, string> | null;
}

interface ProjectCtx {
  projectId: string;
  setProjectId: (id: string) => void;
  projects: ProjectInfo[];
  loading: boolean;
}

const KEY = 'bd.projectId';
const Ctx = createContext<ProjectCtx>({ projectId: '', setProjectId: () => {}, projects: [], loading: true });

export function ProjectProvider({ children }: { children: ReactNode }) {
  const q = useQuery({ queryKey: ['projects'], queryFn: () => projectsApi.listProjects() });
  const projects = q.data?.projects ?? [];
  const [stored, setStored] = useState(() => localStorage.getItem(KEY) ?? '');
  // '' means "All projects" (no scope) — the default. A stored non-empty id is honored only while it's
  // still in the roster; anything else (including a since-removed project) falls back to All.
  const projectId = stored && projects.some((p) => p.projectId === stored) ? stored : '';
  useEffect(() => {
    localStorage.setItem(KEY, projectId);
  }, [projectId]);
  const setProjectId = (id: string) => {
    localStorage.setItem(KEY, id);
    setStored(id);
  };
  return <Ctx.Provider value={{ projectId, setProjectId, projects, loading: q.isLoading }}>{children}</Ctx.Provider>;
}

export function useProject(): ProjectCtx {
  return useContext(Ctx);
}

/**
 * THE ONE PROJECT A SCREEN THAT NEEDS EXACTLY ONE WORKS ON (P-434). Settings and On-call configure a
 * single project, so they cannot show "All projects". They read and write the sidebar's selection
 * instead of keeping their own: picking a project in the sidebar changes theirs, and their picker
 * changes the sidebar's. While the sidebar says "All projects" they take the first project, as they
 * always have, and make it the shared selection so the sidebar names the project on screen.
 */
export function useSingleProject(): ProjectCtx {
  const ctx = useProject();
  const first = ctx.projects[0]?.projectId ?? '';
  const { projectId: shared, setProjectId } = ctx;
  useEffect(() => {
    if (!shared && first) setProjectId(first);
  }, [shared, first, setProjectId]);
  return { ...ctx, projectId: shared || first };
}
