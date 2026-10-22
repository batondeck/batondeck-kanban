/**
 * Global project selection. Fetches the roster via the same ['projects'] react-query key the screens
 * use (shared cache, no extra request), persists the pick to localStorage, and validates it against
 * the list (falls back to the first project). No visual output — the sidebar switcher renders it.
 */
import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from './api.js';

export interface ProjectInfo {
  projectId: string;
  name: string;
  role: string;
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
  const q = useQuery({ queryKey: ['projects'], queryFn: () => api.listProjects() });
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
