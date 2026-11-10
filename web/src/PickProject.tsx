/**
 * WHAT A ONE-PROJECT SCREEN SHOWS WHILE THE SIDEBAR SAYS "ALL PROJECTS" (P-457).
 *
 * Analytics, Reports, the activity feed and the Agents roster each show one project's data, the
 * sidebar's. "All projects" is the sidebar's default after a first sign-in, and on it these screens
 * said "No projects yet" or "No activity yet" although a project existed: they read "no project
 * chosen" as "no project". Settings and On-call already ask for one (P-442); these do too, and offer
 * the caller's projects as one-click choices, which set the sidebar's selection as its own picker does.
 *
 * With no project at all the message is the old one, now true, with the way to make one.
 */
import { Link } from 'react-router';
import { WaitLoader } from './brand.js';
import { EmptyState } from './components.js';
import { useProject } from './projectContext.js';

export function PickProject({ what, none }: {
  /** What the screen shows for a project, as the subject of "… cover one project at a time": "Analytics". */
  what: string;
  /** The sentence for an account with no project yet. */
  none: string;
}) {
  const { projects, setProjectId, loading } = useProject();
  if (loading) return <WaitLoader />;
  if (projects.length === 0) {
    return <EmptyState title="No projects yet" hint={none} action={<Link className="btn" to="/projects">Go to Projects</Link>} />;
  }
  return (
    <EmptyState
      title="Pick a project"
      hint={`${what} cover one project at a time. Choose one here, or in the sidebar.`}
      action={
        <div role="group" aria-label="Projects" style={{ display: 'flex', gap: 8, flexWrap: 'wrap', justifyContent: 'center' }}>
          {projects.map((p) => (
            <button key={p.projectId} className="btn secondary" onClick={() => setProjectId(p.projectId)}>{p.name}</button>
          ))}
        </div>
      }
    />
  );
}
