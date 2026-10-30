/** Status-aware agent picker, shared by the board card (assignee) and the drawer (assignee/handoff).
 *  Shows each agent's live state dot (available/busy/offline/connecting), accepts a free-typed name,
 *  and opens a fixed-positioned menu so it can't be clipped by a scroll container. */
import { useEffect, useRef, useState } from 'react';
import { Icon } from './icons.js';
import { type AgentEntry, type AgentState, STATE_META, agentKey, isLiveEntry } from './agentStatus.js';
import { Avatar } from './components.js';
import { ToolLogo } from './ToolLogo.js';

export function StatusDot({ state, size = 8 }: { state: AgentState; size?: number }) {
  return (
    <span
      className="status-dot"
      data-state={state}
      style={{ width: size, height: size, background: STATE_META[state].color }}
      title={STATE_META[state].label}
      aria-label={STATE_META[state].label}
    />
  );
}

export function AgentSelect({ agents, value, onChange, placeholder = 'agent', allowClear, style }: {
  agents: AgentEntry[];
  value: string;
  onChange: (name: string) => void;
  placeholder?: string;
  allowClear?: boolean;
  style?: React.CSSProperties;
}) {
  const [open, setOpen] = useState(false);
  const [coords, setCoords] = useState<{ top: number; left: number; width: number } | null>(null);
  const [typed, setTyped] = useState('');
  const btnRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent): void => {
      const n = e.target as Node;
      if (btnRef.current?.contains(n) || menuRef.current?.contains(n)) return;
      setOpen(false);
    };
    const onKey = (e: KeyboardEvent): void => { if (e.key === 'Escape') setOpen(false); };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => { document.removeEventListener('mousedown', onDoc); document.removeEventListener('keydown', onKey); };
  }, [open]);

  const toggle = (): void => {
    if (open) { setOpen(false); return; }
    const r = btnRef.current?.getBoundingClientRect();
    if (r) setCoords({ top: r.bottom + 5, left: r.left, width: Math.max(220, r.width) });
    setOpen(true);
  };
  const choose = (name: string): void => { onChange(name); setOpen(false); setTyped(''); };
  const cur = agents.find((a) => a.name === value);
  const menuAgents = agents.filter((a) => isLiveEntry(a, Date.now()));

  return (
    <span style={{ position: 'relative', display: 'inline-block', ...style }}>
      <button ref={btnRef} type="button" className="input" onClick={toggle} style={{ width: '100%', display: 'flex', alignItems: 'center', gap: 7, textAlign: 'left' }}>
        {value ? (
          <>
            {cur && <StatusDot state={cur.state} size={7} />}
            {cur?.tool && <ToolLogo tool={cur.tool} size={12} />}
            <span className="assign-name" style={{ flex: 1 }}>{value.split('@')[0]}</span>
          </>
        ) : (
          <span className="muted" style={{ flex: 1 }}>{placeholder}</span>
        )}
        <span aria-hidden="true" style={{ color: 'var(--text-muted)', display: 'inline-flex' }}><Icon name="chevronDown" size={10} /></span>
      </button>
      {open && coords && (
        <div ref={menuRef} className="assign-menu" role="menu" style={{ top: coords.top, left: coords.left, width: coords.width }}>
          <div className="assign-head">Agents</div>
          <div className="assign-list">
            {menuAgents.length === 0 && <div className="assign-empty">No agents online — type a name to assign.</div>}
            {menuAgents.map((a) => (
              <button key={agentKey(a.identityId, a.name)} type="button" className="assign-item" role="menuitem" onClick={() => choose(a.name)} title={STATE_META[a.state].label}>
                <StatusDot state={a.state} size={7} />
                <Avatar id={a.name} tool={a.tool} />
                <span className="assign-name">{a.name.split('@')[0]}</span>
                {value === a.name && <span className="assign-check"><Icon name="check" size={12} /></span>}
              </button>
            ))}
          </div>
          <div className="assign-type">
            <input
              className="input"
              placeholder="agent name / email"
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter' && typed.trim()) choose(typed.trim()); }}
            />
            <button type="button" className="btn" disabled={!typed.trim()} onClick={() => choose(typed.trim())}>Use</button>
          </div>
          {allowClear && value && <button type="button" className="assign-item unassign" onClick={() => choose('')}>Clear</button>}
        </div>
      )}
    </span>
  );
}
