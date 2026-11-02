import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useCan, useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { StatusText, Tone } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { ago } from '../../lib/format'
import { useLive } from '../../lib/live'
import { Server } from '../../lib/types'
import { Monitor } from '../monitors/Monitors'
import { App } from '../services/serviceHelpers'
import { TEMPLATES, WorkflowSummary, triggerSummary } from './workflowHelpers'

export const runTone = (s: string): Tone =>
  s === 'succeeded' ? 'ok' : s === 'failed' ? 'err' : s === 'running' ? 'info' : ''

export const runLabel = (s: string) => s[0].toUpperCase() + s.slice(1)

/** Names of the project's services, monitors and servers by id, for summaries and pickers. */
export function useRefs() {
  const { project } = useProject()
  const [apps] = useApi<App[]>(`/projects/${project.id}/apps`)
  const [monitors] = useApi<Monitor[]>(`/projects/${project.id}/monitors`)
  const [servers] = useApi<Server[]>(`/projects/${project.id}/servers`)
  const names: Record<string, string> = {}
  for (const x of [...(apps ?? []), ...(monitors ?? []), ...(servers ?? [])]) names[x.id] = x.name
  return {
    apps: apps ?? [],
    monitors: monitors ?? [],
    servers: servers ?? [],
    names,
    loaded: !!(apps && monitors && servers),
  }
}

export function EnabledSwitch({ id, enabled, onChange }: { id: string; enabled: boolean; onChange: () => void }) {
  const { project } = useProject()
  const canEdit = useCan('admin')
  const [error, setError] = useState<string>()
  return (
    <label className="row nowrap" style={{ gap: 6 }} onClick={(e) => e.stopPropagation()} title={error}>
      <input
        type="checkbox"
        role="switch"
        checked={enabled}
        disabled={!canEdit}
        onChange={(e) =>
          api('POST', `/projects/${project.id}/workflows/${id}/enabled`, { enabled: e.target.checked }).then(
            onChange,
            (err) => setError(err.message),
          )
        }
      />
      <span className={enabled ? '' : 'muted'}>{enabled ? 'On' : 'Off'}</span>
    </label>
  )
}

export function Workflows() {
  const { project } = useProject()
  const nav = useNavigate()
  const canEdit = useCan('admin')
  const [list, error, reload] = useApi<WorkflowSummary[]>(`/projects/${project.id}/workflows`)
  const { names } = useRefs()
  const [choosing, setChoosing] = useState(false)
  useLive(project.id, ['workflows'], reload)
  return (
    <div className="page">
      <PageHead
        title="Workflows"
        sub="Steps that run on their own when something happens: a deploy, an outage, a schedule"
      >
        {canEdit && (
          <button className="btn primary" onClick={() => setChoosing(true)}>
            New workflow
          </button>
        )}
      </PageHead>
      <ErrorNote error={error} />
      <Panel>
        {!list ? (
          <Loading />
        ) : !list.length ? (
          <Empty title="No workflows yet">
            Restart a service when its monitor goes down, post deploys to chat, or page on-call when a server stops
            reporting. Start from a template.
          </Empty>
        ) : (
          <table className="t">
            <thead>
              <tr>
                <th>Workflow</th>
                <th>Last run</th>
                <th className="right">Switched</th>
              </tr>
            </thead>
            <tbody>
              {list.map((w) => (
                <tr key={w.id} className="link" onClick={() => nav(`/p/${project.id}/workflows/${w.id}`)}>
                  <td>
                    <div className="name">{w.name}</div>
                    <div className="sub">{triggerSummary(w.trigger, names)}</div>
                  </td>
                  <td>
                    {w.last_run ? (
                      <StatusText tone={runTone(w.last_run.status)}>
                        {runLabel(w.last_run.status)} <span className="muted">· {ago(w.last_run.created_at)}</span>
                      </StatusText>
                    ) : (
                      <span className="muted">Never run</span>
                    )}
                  </td>
                  <td className="right">
                    <EnabledSwitch id={w.id} enabled={w.enabled} onChange={reload} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
      {choosing && (
        <Dialog title="New workflow" onClose={() => setChoosing(false)}>
          <div className="list">
            {TEMPLATES.map((t) => (
              <button
                key={t.key}
                className="wf-node"
                onClick={() => nav(`/p/${project.id}/workflows/new?template=${t.key}`)}
              >
                <b>{t.name}</b>
                <span className="muted">{t.description}</span>
              </button>
            ))}
          </div>
        </Dialog>
      )}
    </div>
  )
}
