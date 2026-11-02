import {
  Activity,
  AppWindow,
  Archive,
  Bell,
  HeartPulse,
  Radio,
  Boxes,
  Building2,
  Container as ContainerIcon,
  Database,
  FolderKanban,
  GitFork,
  History,
  Key,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Moon,
  Network,
  Plug,
  Rocket,
  ScrollText,
  Server as ServerIcon,
  Sun,
  Tag,
  UserRound,
  Users,
  type LucideIcon,
} from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { NavLink, useNavigate } from 'react-router'
import { api } from '../../lib/api'
import { Org, useProject } from './context'
import { Check, Chevron } from './icons'
import { NewProject } from './NewProjectDialog'
import { setTheme } from './theme'

const NAV: { label: string; items: [string, string, LucideIcon][] }[] = [
  { label: '', items: [['Overview', 'overview', LayoutDashboard]] },
  {
    label: 'Infrastructure',
    items: [
      ['Servers', 'servers', ServerIcon],
      ['Clusters', 'clusters', Network],
      ['Docker', 'docker', ContainerIcon],
      ['Nomad', 'nomad', Boxes],
    ],
  },
  {
    label: 'Applications',
    items: [
      ['Services', 'services', AppWindow],
      ['Deployments', 'deployments', Rocket],
      ['Releases', 'releases', Tag],
    ],
  },
  {
    label: 'Databases',
    items: [
      ['Databases', 'databases', Database],
      ['Replicas', 'replicas', GitFork],
      ['Backups', 'backups', Archive],
    ],
  },
  {
    label: 'Observability',
    items: [
      ['Metrics', 'metrics', Activity],
      ['Logs', 'logs', ScrollText],
      ['Events', 'events', History],
      ['Monitors', 'monitors', HeartPulse],
      ['Alerts', 'alerts', Bell],
      ['Status pages', 'status-pages', Radio],
    ],
  },
  {
    label: 'Security',
    items: [
      ['Secrets', 'secrets', KeyRound],
      ['Access', 'access', Users],
      ['API Keys', 'api-keys', Key],
    ],
  },
  {
    label: 'Settings',
    items: [
      ['Organization', 'settings/organization', Building2],
      ['Projects', 'settings/projects', FolderKanban],
      ['Agents', 'settings/agents', Plug],
      ['Account', 'settings/account', UserRound],
    ],
  },
]

export function Sidebar() {
  const { me, org, project } = useProject()
  const [open, setOpen] = useState(false)
  const [creating, setCreating] = useState<Org | null>(null)
  const [theme, setT] = useState(document.documentElement.dataset.theme ?? 'dark')
  const nav = useNavigate()
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const close = (e: MouseEvent) => ref.current && !ref.current.contains(e.target as Node) && setOpen(false)
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', esc)
    return () => {
      document.removeEventListener('mousedown', close)
      document.removeEventListener('keydown', esc)
    }
  }, [])

  return (
    <aside className="sidebar">
      <div className="brand">
        <img src="/brand/icon.svg" alt="" width={22} height={17} />
        <span>
          Nolyx <b>Orbit</b>
        </span>
      </div>
      <div ref={ref} className="switcher-wrap">
        <button className="switcher" onClick={() => setOpen(!open)} aria-haspopup="menu" aria-expanded={open}>
          <span className="proj-avatar" aria-hidden>
            {project.name[0]?.toUpperCase()}
          </span>
          <span style={{ flex: 1, minWidth: 0 }}>
            <div className="org">{org.name}</div>
            <div className="proj">{project.name}</div>
          </span>
          <Chevron />
        </button>
        <div className={`menu switcher-menu ${open ? 'open' : ''}`} role="menu" aria-hidden={!open}>
          {me.orgs.map((o, i) => (
            <div key={o.id}>
              {i > 0 && <hr />}
              <div className="label">{o.name}</div>
              {o.projects.map((p) => (
                <button
                  key={p.id}
                  role="menuitem"
                  tabIndex={open ? 0 : -1}
                  className={p.id === project.id ? 'current' : ''}
                  onClick={() => {
                    setOpen(false)
                    nav(`/p/${p.id}/overview`)
                  }}
                >
                  <span className="proj-avatar sm" aria-hidden>
                    {p.name[0]?.toUpperCase()}
                  </span>
                  <span className="name">{p.name}</span>
                  {p.id === project.id && <Check />}
                </button>
              ))}
              {(o.role === 'owner' || o.role === 'admin') && (
                <button
                  role="menuitem"
                  tabIndex={open ? 0 : -1}
                  className="muted"
                  onClick={() => {
                    setOpen(false)
                    setCreating(o)
                  }}
                >
                  <span className="proj-avatar sm add" aria-hidden>
                    +
                  </span>
                  <span className="name">New project</span>
                </button>
              )}
            </div>
          ))}
        </div>
      </div>
      <nav className="nav">
        {NAV.map((g) => (
          <div key={g.label} className="nav-group">
            {g.label && <div className="nav-label">{g.label}</div>}
            {g.items.map(([label, path, Icon]) => (
              <NavLink key={path} to={`/p/${project.id}/${path}`}>
                <Icon size={17} strokeWidth={1.75} aria-hidden />
                {label}
              </NavLink>
            ))}
          </div>
        ))}
      </nav>
      <div className="sidebar-foot">
        <span className="user-avatar" aria-hidden>
          {initials(me.user.name || me.user.email)}
        </span>
        <span className="who" title={me.user.email}>
          <span className="who-name">{me.user.name || me.user.email}</span>
          <span className="who-role">{project.role}</span>
        </span>
        <button
          className="icon-btn"
          title={theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
          aria-label="Toggle theme"
          onClick={() => {
            const t = theme === 'dark' ? 'light' : 'dark'
            setTheme(t)
            setT(t)
          }}
        >
          {theme === 'dark' ? <Moon size={17} strokeWidth={1.75} /> : <Sun size={17} strokeWidth={1.75} />}
        </button>
        <button
          className="icon-btn"
          title="Sign out"
          aria-label="Sign out"
          onClick={() => api('POST', '/auth/logout').then(() => (window.location.href = '/login'))}
        >
          <LogOut size={17} strokeWidth={1.75} />
        </button>
      </div>
      {creating && <NewProject org={creating} onClose={() => setCreating(null)} />}
    </aside>
  )
}

function initials(name: string) {
  const parts = name
    .trim()
    .split(/[\s@._-]+/)
    .filter(Boolean)
  return ((parts[0]?.[0] ?? '') + (parts.length > 1 ? parts[1][0] : '')).toUpperCase() || '?'
}
