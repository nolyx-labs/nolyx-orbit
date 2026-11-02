import { Fragment, useEffect, useState } from 'react'
import { Link, NavLink, useNavigate, useParams, useSearchParams } from 'react-router'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Dialog } from '../../components/ui/Dialog'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Copy } from '../../components/ui/controls'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { ago, datetime, duration } from '../../lib/format'
import { useLive } from '../../lib/live'
import {
  CONDITIONS,
  Run,
  RunStep,
  STEPS,
  TEMPLATES,
  TRIGGERS,
  WEEKDAYS,
  WfCond,
  WfDef,
  WfStep,
  WfTrigger,
  Workflow,
  canFail,
  getAt,
  insertAt,
  newStep,
  setAt,
  stepSummary,
  triggerSummary,
} from './workflowHelpers'
import { EnabledSwitch, runLabel, runTone, useRefs } from './Workflows'

type Path = (string | number)[]
type Refs = ReturnType<typeof useRefs>

const PLACEHOLDERS = '{{service}} {{server}} {{monitor}} {{trigger}} {{run_url}} {{data.field}}'

export function WorkflowDetail() {
  const { project, me } = useProject()
  const { id = 'new', tab = 'steps', rid } = useParams()
  const [search] = useSearchParams()
  const nav = useNavigate()
  const isNew = id === 'new'
  const [wf, loadError, reload] = useApi<Workflow>(isNew ? null : `/projects/${project.id}/workflows/${id}`)
  const refs = useRefs()
  const [channels] = useApi<{ channels: { id: string }[] }>(isNew ? `/projects/${project.id}/channels` : null)
  const [draft, setDraft] = useState<{ name: string; def: WfDef } | null>(null)
  const [dirty, setDirty] = useState(false)
  const canEdit = useCan('admin')
  const canRun = useCan('member')
  const [error, setError] = useState<string>()
  const [notice, setNotice] = useState<string>()
  const [deleting, setDeleting] = useState(false)
  useLive(project.id, ['workflows'], () => !dirty && reload())

  // Start the draft from the saved workflow, or from the chosen template once the project's items are loaded.
  const ready = isNew ? !!channels && refs.loaded : !!wf
  useEffect(() => {
    if (!ready || (draft && dirty)) return
    if (wf) setDraft({ name: wf.name, def: wf.definition })
    else if (isNew && !draft) {
      const t = TEMPLATES.find((t) => t.key === search.get('template')) ?? TEMPLATES[TEMPLATES.length - 1]
      const ids = channels!.channels.map((c) => c.id)
      const to = ids.length ? { channel_ids: ids, user_ids: [] } : { channel_ids: [], user_ids: [me.user.id] }
      setDraft({ name: t.name, def: t.build({ service: refs.apps[0]?.id, monitor: refs.monitors[0]?.id, to }) })
      setDirty(true)
    }
  }, [ready, wf])

  if (loadError)
    return (
      <div className="page">
        <ErrorNote error={loadError} />
      </div>
    )
  if (!draft) return <Loading />
  const base = `/p/${project.id}/workflows/${id}`
  const update = (name: string, def: WfDef) => {
    setDraft({ name, def })
    setDirty(true)
  }
  const save = () =>
    api<{ id: string }>(
      isNew ? 'POST' : 'PUT',
      isNew ? `/projects/${project.id}/workflows` : `/projects/${project.id}/workflows/${id}`,
      {
        name: draft.name,
        definition: draft.def,
      },
    ).then(
      (r) => {
        setDirty(false)
        setError(undefined)
        if (isNew) nav(`/p/${project.id}/workflows/${r.id}`, { replace: true })
        else reload()
      },
      (e) => setError(e.message),
    )
  const run = () =>
    api<{ id: string; skipped: boolean }>('POST', `/projects/${project.id}/workflows/${id}/run`).then(
      (r) => (r.skipped ? setNotice('Not started: the previous run is still going.') : nav(`${base}/runs/${r.id}`)),
      (e) => setError(e.message),
    )

  return (
    <div className="page">
      <PageHead
        crumbs={<Link to={`/p/${project.id}/workflows`}>Workflows</Link>}
        title={draft.name || 'Untitled workflow'}
        sub={
          isNew
            ? 'Not saved yet. New workflows start switched off.'
            : `Last saved by ${wf?.saved_by ?? 'a removed user'}; its steps run with that person's access.`
        }
      >
        {!isNew && wf && <EnabledSwitch id={wf.id} enabled={wf.enabled} onChange={reload} />}
        {!isNew && canRun && (
          <button className="btn" onClick={run} disabled={dirty} title={dirty ? 'Save your changes first' : ''}>
            Run now
          </button>
        )}
        {canEdit && (
          <button className="btn primary" onClick={save} disabled={!dirty}>
            {isNew ? 'Create' : 'Save'}
          </button>
        )}
        {!isNew && canEdit && (
          <button className="btn danger" onClick={() => setDeleting(true)}>
            Delete
          </button>
        )}
      </PageHead>
      <ErrorNote error={error} />
      {notice && <div className="callout warn">{notice}</div>}
      {!isNew && (
        <div className="tabs">
          <NavLink to={`${base}/steps`} className={tab === 'steps' && !rid ? 'active' : ''}>
            Steps
          </NavLink>
          <NavLink to={`${base}/runs`} className={tab === 'runs' || rid ? 'active' : ''}>
            Runs
          </NavLink>
        </div>
      )}
      {rid ? (
        <RunView workflowId={id} runId={rid} />
      ) : tab === 'runs' && !isNew ? (
        <Runs workflowId={id} />
      ) : (
        <Editor
          name={draft.name}
          def={draft.def}
          webhookURL={wf?.webhook_url ?? null}
          refs={refs}
          readOnly={!canEdit}
          onChange={update}
        />
      )}
      {deleting && (
        <Confirm
          title={`Delete ${draft.name}?`}
          action="Delete"
          danger
          onConfirm={() =>
            api('DELETE', `/projects/${project.id}/workflows/${id}`).then(() => nav(`/p/${project.id}/workflows`))
          }
          onClose={() => setDeleting(false)}
        >
          Its run history is deleted too.
        </Confirm>
      )}
    </div>
  )
}

// ---------- editor ----------

function Editor({
  name,
  def,
  webhookURL,
  refs,
  readOnly,
  onChange,
}: {
  name: string
  def: WfDef
  webhookURL: string | null
  refs: Refs
  readOnly: boolean
  onChange: (name: string, def: WfDef) => void
}) {
  const [selected, setSelected] = useState<Path>([])
  const [adding, setAdding] = useState<{ list: Path; index: number } | null>(null)
  const key = selected.join('.')
  const node = getAt(def, selected)

  const add = (type: string) => {
    const { list, index } = adding!
    onChange(
      name,
      insertAt(def, list, index, newStep(type, { service: refs.apps[0]?.id, monitor: refs.monitors[0]?.id })),
    )
    setSelected([...list, index])
    setAdding(null)
  }
  // Plain render functions rather than components, so re-renders don't remount the tree.
  const addButton = (list: Path, index: number) =>
    readOnly ? null : (
      <button className="wf-add" onClick={() => setAdding({ list, index })}>
        + Add a step
      </button>
    )
  const renderList = (path: Path, steps: WfStep[]) => (
    <div className="wf-list">
      {!steps.length && addButton(path, 0)}
      {steps.map((st, i) => {
        const p = [...path, i]
        return (
          <Fragment key={i}>
            <button className={`wf-node ${p.join('.') === key ? 'on' : ''}`} onClick={() => setSelected(p)}>
              <b>{STEPS.find((s) => s[0] === st.type)?.[1] ?? st.type}</b>
              <span className="muted">{stepSummary(st, refs.names)}</span>
            </button>
            {st.type === 'if' && (
              <>
                <div className="wf-branch">
                  <div className="wf-label">If true</div>
                  {renderList([...p, 'then'], st.then ?? [])}
                </div>
                <div className="wf-branch">
                  <div className="wf-label">Otherwise</div>
                  {renderList([...p, 'else'], st.else ?? [])}
                </div>
              </>
            )}
            {canFail(st.type) && (
              <div className="wf-branch fail">
                <div className="wf-label">
                  If it fails{st.on_failure?.length ? ', then carry on' : ': stop and tell the project’s channels'}
                </div>
                {(st.on_failure?.length || !readOnly) && renderList([...p, 'on_failure'], st.on_failure ?? [])}
              </div>
            )}
            {addButton(path, i + 1)}
          </Fragment>
        )
      })}
    </div>
  )

  return (
    <div className="split">
      <Panel pad>
        <button className={`wf-node trigger ${key === '' ? 'on' : ''}`} onClick={() => setSelected([])}>
          <b>When</b>
          <span className="muted">{triggerSummary(def.trigger, refs.names)}</span>
        </button>
        {renderList(['steps'], def.steps)}
      </Panel>
      <Panel title={selected.length ? 'Step' : 'Trigger'} pad>
        <fieldset disabled={readOnly} className="form" style={{ border: 0, padding: 0, margin: 0 }} key={key}>
          {!selected.length ? (
            <TriggerEditor
              name={name}
              trigger={def.trigger}
              webhookURL={webhookURL}
              refs={refs}
              onName={(n) => onChange(n, def)}
              onChange={(t) => onChange(name, { ...def, trigger: t })}
            />
          ) : node ? (
            <>
              <StepEditor step={node} refs={refs} onChange={(st) => onChange(name, setAt(def, selected, st))} />
              <div className="row" style={{ gap: 8 }}>
                <button
                  className="btn sm"
                  disabled={selected[selected.length - 1] === 0}
                  onClick={() => {
                    const i = selected[selected.length - 1] as number
                    const list = selected.slice(0, -1)
                    const moved = insertAt(setAt(def, selected, undefined), list, i - 1, node)
                    onChange(name, moved)
                    setSelected([...list, i - 1])
                  }}
                >
                  Move up
                </button>
                <div className="spacer" />
                <button
                  className="btn sm danger"
                  onClick={() => {
                    onChange(name, setAt(def, selected, undefined))
                    setSelected([])
                  }}
                >
                  Remove step
                </button>
              </div>
            </>
          ) : null}
          <span className="hint faint">Placeholders you can use in text: {PLACEHOLDERS}</span>
        </fieldset>
      </Panel>
      {adding && (
        <Dialog title="Add a step" onClose={() => setAdding(null)}>
          <div className="list">
            {STEPS.map(([type, label]) => (
              <button key={type} className="wf-node" onClick={() => add(type)}>
                <b>{label}</b>
              </button>
            ))}
          </div>
        </Dialog>
      )}
    </div>
  )
}

function Select({
  label,
  value,
  onChange,
  options,
  any,
}: {
  label: string
  value: string | undefined
  onChange: (v: string) => void
  options: { id: string; name: string }[]
  any?: string
}) {
  return (
    <label className="field">
      <span>{label}</span>
      <select className="select" value={value ?? ''} onChange={(e) => onChange(e.target.value)}>
        <option value="">{any ?? 'Choose…'}</option>
        {options.map((o) => (
          <option key={o.id} value={o.id}>
            {o.name}
          </option>
        ))}
      </select>
    </label>
  )
}

function TriggerEditor({
  name,
  trigger: t,
  webhookURL,
  refs,
  onName,
  onChange,
}: {
  name: string
  trigger: WfTrigger
  webhookURL: string | null
  refs: Refs
  onName: (n: string) => void
  onChange: (t: WfTrigger) => void
}) {
  const scope = TRIGGERS.find((x) => x[0] === t.type)?.[2] ?? []
  const set = (patch: Partial<WfTrigger>) => onChange({ ...t, ...patch })
  return (
    <>
      <label className="field">
        <span>Workflow name</span>
        <input className="input" value={name} onChange={(e) => onName(e.target.value)} />
      </label>
      <label className="field">
        <span>Runs when</span>
        <select
          className="select"
          value={t.type}
          onChange={(e) =>
            onChange(
              e.target.value === 'schedule'
                ? { type: 'schedule', every: 'daily', at: '03:00' }
                : { type: e.target.value },
            )
          }
        >
          {TRIGGERS.map(([type, label]) => (
            <option key={type} value={type}>
              {label}
            </option>
          ))}
        </select>
      </label>
      {t.type === 'schedule' && (
        <div className="grid-2">
          <label className="field">
            <span>Every</span>
            <select className="select" value={t.every} onChange={(e) => set({ every: e.target.value, weekday: 0 })}>
              <option value="hourly">Hour</option>
              <option value="daily">Day</option>
              <option value="weekly">Week</option>
            </select>
          </label>
          <label className="field">
            <span>{t.every === 'hourly' ? 'Minutes past the hour' : 'At (UTC)'}</span>
            <input className="input" type="time" value={t.at} onChange={(e) => set({ at: e.target.value })} />
          </label>
          {t.every === 'weekly' && (
            <label className="field">
              <span>On</span>
              <select className="select" value={t.weekday ?? 0} onChange={(e) => set({ weekday: +e.target.value })}>
                {WEEKDAYS.map((d, i) => (
                  <option key={d} value={i}>
                    {d}
                  </option>
                ))}
              </select>
            </label>
          )}
        </div>
      )}
      {scope.includes('service_id') && (
        <Select
          label="Service"
          value={t.service_id}
          onChange={(v) => set({ service_id: v })}
          options={t.type === 'github_push' ? refs.apps.filter((a) => a.repo_url) : refs.apps}
          any={t.type === 'github_push' ? 'Choose a service connected to GitHub…' : 'Any service'}
        />
      )}
      {scope.includes('branch') && (
        <label className="field">
          <span>Branch</span>
          <input
            className="input mono"
            value={t.branch ?? ''}
            placeholder="Any branch"
            onChange={(e) => set({ branch: e.target.value })}
          />
          <span className="hint">The service's GitHub webhook must be set up (Services → the service → Git).</span>
        </label>
      )}
      {scope.includes('status') && (
        <label className="field">
          <span>Result</span>
          <select className="select" value={t.status ?? ''} onChange={(e) => set({ status: e.target.value })}>
            <option value="">Succeeded or failed</option>
            <option value="succeeded">Succeeded</option>
            <option value="failed">Failed</option>
          </select>
        </label>
      )}
      {scope.includes('monitor_id') && (
        <Select
          label="Monitor"
          value={t.monitor_id}
          onChange={(v) => set({ monitor_id: v })}
          options={refs.monitors}
          any="Any monitor"
        />
      )}
      {scope.includes('server_id') && (
        <Select
          label="Server"
          value={t.server_id}
          onChange={(v) => set({ server_id: v })}
          options={refs.servers}
          any="Any server"
        />
      )}
      {t.type === 'disk_full' && (
        <span className="hint">Fires once when a disk reaches 90% used, and again only after it drops below 85%.</span>
      )}
      {t.type === 'service_restarting' && <span className="hint">Fires after 3 restarts within 15 minutes.</span>}
      {t.type === 'backup_failed' && <span className="hint">Fires when a database backup fails.</span>}
      {t.type === 'webhook' && (
        <div className="field">
          <span>Webhook URL</span>
          {webhookURL ? (
            <div className="row" style={{ gap: 8 }}>
              <code className="mono" style={{ wordBreak: 'break-all', flex: 1 }}>
                {webhookURL}
              </code>
              <Copy text={webhookURL} />
            </div>
          ) : (
            <span className="hint">Save to get a secret URL.</span>
          )}
          <span className="hint">
            POST JSON to it while the workflow is on. Its fields are available to steps as {'{{data.field}}'}.
          </span>
        </div>
      )}
    </>
  )
}

function DurationInput({
  label,
  seconds,
  onChange,
}: {
  label: string
  seconds: number
  onChange: (s: number) => void
}) {
  const unit = seconds % 3600 === 0 && seconds >= 3600 ? 3600 : seconds % 60 === 0 && seconds >= 60 ? 60 : 1
  return (
    <label className="field">
      <span>{label}</span>
      <div className="row" style={{ gap: 8 }}>
        <input
          className="input"
          type="number"
          min={1}
          value={seconds / unit}
          onChange={(e) => onChange(Math.max(0, Math.round(+e.target.value)) * unit)}
        />
        <select className="select" value={unit} onChange={(e) => onChange((seconds / unit) * +e.target.value)}>
          <option value={1}>seconds</option>
          <option value={60}>minutes</option>
          <option value={3600}>hours</option>
        </select>
      </div>
    </label>
  )
}

function StepEditor({ step: st, refs, onChange }: { step: WfStep; refs: Refs; onChange: (s: WfStep) => void }) {
  const { project, org } = useProject()
  const set = (patch: Partial<WfStep>) => onChange({ ...st, ...patch })
  const [channels] = useApi<{ channels: { id: string; name: string; type: string }[] }>(
    st.type === 'alert' ? `/projects/${project.id}/channels` : null,
  )
  const [orgData] = useApi<{ members: { id: string; email: string }[] }>(st.type === 'alert' ? `/orgs/${org.id}` : null)
  const [headers, setHeaders] = useState(
    Object.entries(st.headers ?? {})
      .map(([k, v]) => `${k}: ${v}`)
      .join('\n'),
  )
  const toggle = (list: string[] | undefined, v: string) =>
    list?.includes(v) ? list.filter((x) => x !== v) : [...(list ?? []), v]
  const service = (
    <Select label="Service" value={st.service_id} onChange={(v) => set({ service_id: v })} options={refs.apps} />
  )
  switch (st.type) {
    case 'redeploy':
      return (
        <>
          {service}
          <span className="hint">
            Re-runs the service's last deploy. When a GitHub push to the service's repository started this run, deploys
            the pushed commit instead. The step waits for the deploy to finish.
          </span>
        </>
      )
    case 'start':
    case 'stop':
    case 'restart':
      return service
    case 'run_command':
      return (
        <>
          {service}
          <label className="field">
            <span>Command</span>
            <textarea
              className="input mono"
              rows={3}
              value={st.command ?? ''}
              onChange={(e) => set({ command: e.target.value })}
            />
            <span className="hint">
              Runs with sh -c in the service's container, as whoever saves this workflow; they need two-factor
              authentication turned on. Placeholders aren't filled in here.
            </span>
          </label>
          <label className="field">
            <span>Time limit (seconds, up to 60)</span>
            <input
              className="input"
              type="number"
              min={1}
              max={60}
              value={st.seconds || 30}
              onChange={(e) => set({ seconds: +e.target.value })}
            />
          </label>
        </>
      )
    case 'check_monitor':
      return (
        <>
          <Select
            label="Monitor"
            value={st.monitor_id}
            onChange={(v) => set({ monitor_id: v })}
            options={refs.monitors}
          />
          <DurationInput
            label="Keep checking for up to"
            seconds={st.seconds || 120}
            onChange={(s) => set({ seconds: s })}
          />
          <span className="hint">
            Checks every 10 seconds until it passes. Fails if it is still failing at the end.
          </span>
        </>
      )
    case 'wait':
      return (
        <DurationInput
          label="Wait for (up to 24 hours)"
          seconds={st.seconds ?? 60}
          onChange={(s) => set({ seconds: s })}
        />
      )
    case 'http':
      return (
        <>
          <div className="grid-2" style={{ gridTemplateColumns: '110px 1fr' }}>
            <label className="field">
              <span>Method</span>
              <select className="select" value={st.method ?? 'POST'} onChange={(e) => set({ method: e.target.value })}>
                {['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD'].map((m) => (
                  <option key={m}>{m}</option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>URL</span>
              <input className="input mono" value={st.url ?? ''} onChange={(e) => set({ url: e.target.value })} />
            </label>
          </div>
          <label className="field">
            <span>Headers (one per line, Name: value)</span>
            <textarea
              className="input mono"
              rows={2}
              value={headers}
              onChange={(e) => {
                setHeaders(e.target.value)
                const h: Record<string, string> = {}
                for (const line of e.target.value.split('\n')) {
                  const i = line.indexOf(':')
                  if (i > 0) h[line.slice(0, i).trim()] = line.slice(i + 1).trim()
                }
                set({ headers: h })
              }}
            />
          </label>
          <label className="field">
            <span>Body (sent as JSON)</span>
            <textarea
              className="input mono"
              rows={5}
              value={st.body ?? ''}
              onChange={(e) => set({ body: e.target.value })}
            />
          </label>
        </>
      )
    case 'alert':
      return (
        <>
          <div className="field">
            <span>Notification channels</span>
            {channels?.channels.length ? (
              channels.channels.map((c) => (
                <label key={c.id} className="row" style={{ gap: 6 }}>
                  <input
                    type="checkbox"
                    checked={st.channel_ids?.includes(c.id) ?? false}
                    onChange={() => set({ channel_ids: toggle(st.channel_ids, c.id) })}
                  />
                  {c.name} <span className="faint">{c.type}</span>
                </label>
              ))
            ) : (
              <span className="hint">This project has no notification channels (Alerts → Channels).</span>
            )}
          </div>
          <div className="field">
            <span>Email members</span>
            {orgData?.members.map((m) => (
              <label key={m.id} className="row" style={{ gap: 6 }}>
                <input
                  type="checkbox"
                  checked={st.user_ids?.includes(m.id) ?? false}
                  onChange={() => set({ user_ids: toggle(st.user_ids, m.id) })}
                />
                {m.email}
              </label>
            ))}
          </div>
          <label className="field">
            <span>Message</span>
            <textarea
              className="input"
              rows={3}
              value={st.message ?? ''}
              onChange={(e) => set({ message: e.target.value })}
            />
          </label>
        </>
      )
    case 'if':
      return <CondEditor cond={st.if!} refs={refs} onChange={(c) => set({ if: c })} />
  }
  return null
}

function CondEditor({ cond: c, refs, onChange }: { cond: WfCond; refs: Refs; onChange: (c: WfCond) => void }) {
  const set = (patch: Partial<WfCond>) => onChange({ ...c, ...patch })
  return (
    <>
      <label className="field">
        <span>Condition</span>
        <select
          className="select"
          value={c.type}
          onChange={(e) =>
            onChange(
              e.target.value === 'time_between'
                ? { type: e.target.value, from: '09:00', to: '17:00' }
                : e.target.value === 'disk_below'
                  ? { type: e.target.value, percent: 80 }
                  : { type: e.target.value },
            )
          }
        >
          {CONDITIONS.map(([type, label]) => (
            <option key={type} value={type}>
              {label}
            </option>
          ))}
        </select>
      </label>
      {(c.type === 'field_equals' || c.type === 'field_contains') && (
        <div className="grid-2">
          <label className="field">
            <span>Field</span>
            <input
              className="input mono"
              value={c.field ?? ''}
              placeholder="data.status"
              onChange={(e) => set({ field: e.target.value })}
            />
          </label>
          <label className="field">
            <span>Value</span>
            <input className="input" value={c.value ?? ''} onChange={(e) => set({ value: e.target.value })} />
          </label>
        </div>
      )}
      {c.type === 'monitor_up' && (
        <Select label="Monitor" value={c.monitor_id} onChange={(v) => set({ monitor_id: v })} options={refs.monitors} />
      )}
      {c.type === 'service_running' && (
        <Select label="Service" value={c.service_id} onChange={(v) => set({ service_id: v })} options={refs.apps} />
      )}
      {c.type === 'disk_below' && (
        <div className="grid-2">
          <Select label="Server" value={c.server_id} onChange={(v) => set({ server_id: v })} options={refs.servers} />
          <label className="field">
            <span>Used below (%)</span>
            <input
              className="input"
              type="number"
              min={1}
              max={100}
              value={c.percent ?? 80}
              onChange={(e) => set({ percent: +e.target.value })}
            />
          </label>
        </div>
      )}
      {c.type === 'time_between' && (
        <div className="grid-2">
          <label className="field">
            <span>From (UTC)</span>
            <input className="input" type="time" value={c.from} onChange={(e) => set({ from: e.target.value })} />
          </label>
          <label className="field">
            <span>To (UTC)</span>
            <input className="input" type="time" value={c.to} onChange={(e) => set({ to: e.target.value })} />
          </label>
        </div>
      )}
    </>
  )
}

// ---------- runs ----------

function Runs({ workflowId }: { workflowId: string }) {
  const { project } = useProject()
  const nav = useNavigate()
  const [runs, , reload] = useApi<Run[]>(`/projects/${project.id}/workflows/${workflowId}/runs`)
  useLive(project.id, ['workflows'], reload)
  return (
    <Panel>
      {!runs ? (
        <Loading />
      ) : !runs.length ? (
        <Empty title="No runs yet">Runs appear here when the trigger fires or someone presses Run now.</Empty>
      ) : (
        <table className="t">
          <thead>
            <tr>
              <th>Status</th>
              <th>Started by</th>
              <th>When</th>
              <th className="right">Took</th>
            </tr>
          </thead>
          <tbody>
            {runs.map((r) => (
              <tr
                key={r.id}
                className="link"
                onClick={() => nav(`/p/${project.id}/workflows/${workflowId}/runs/${r.id}`)}
              >
                <td>
                  <StatusText tone={runTone(r.status)}>{runLabel(r.status)}</StatusText>
                  {r.error && <div className="sub">{r.error}</div>}
                </td>
                <td>
                  {r.trigger === 'manual'
                    ? (r.started_by ?? 'Run by hand')
                    : TRIGGERS.find((t) => t[0] === r.trigger)?.[1]}
                </td>
                <td className="muted">{ago(r.created_at)}</td>
                <td className="right mono">{r.status === 'skipped' ? '—' : duration(r.duration)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}

const branchNote: Record<string, string> = {
  then: 'The condition was true',
  else: 'The condition was false',
  on_failure: 'Ran the “If it fails” steps',
}

function RunView({ workflowId, runId }: { workflowId: string; runId: string }) {
  const { project } = useProject()
  const [run, error, reload] = useApi<Run & { data: Record<string, unknown>; steps: RunStep[] }>(
    `/projects/${project.id}/workflows/${workflowId}/runs/${runId}`,
  )
  useLive(project.id, ['workflows'], reload, 1000)
  if (error) return <ErrorNote error={error} />
  if (!run) return <Loading />
  return (
    <>
      <Panel
        title={
          <StatusText tone={runTone(run.status)}>
            {runLabel(run.status)} · {datetime(run.created_at)}
          </StatusText>
        }
        actions={<span className="muted">{duration(run.duration)}</span>}
      >
        {run.error && (
          <div className="panel-body">
            <ErrorNote error={run.error} />
          </div>
        )}
        <div className="list">
          {run.steps.map((s) => (
            <div
              key={s.id}
              className="list-item"
              style={{ alignItems: 'flex-start', paddingLeft: 14 + 24 * ((s.path.split('.').length - 1) / 2) }}
            >
              <div style={{ flex: 1, minWidth: 0, display: 'grid', gap: 6 }}>
                <div className="row" style={{ gap: 8 }}>
                  <StatusText tone={runTone(s.status)}>{s.title}</StatusText>
                  {branchNote[s.branch] && <span className="muted">· {branchNote[s.branch]}</span>}
                  <div className="spacer" />
                  <span className="muted mono">{duration(s.duration)}</span>
                </div>
                {s.error && (
                  <pre className="code" style={{ whiteSpace: 'pre-wrap', color: 'var(--err)' }}>
                    {s.error}
                  </pre>
                )}
                {s.output && (
                  <pre className="code" style={{ whiteSpace: 'pre-wrap' }}>
                    {s.output}
                  </pre>
                )}
              </div>
            </div>
          ))}
          {!run.steps.length && <Empty title="No steps ran" />}
        </div>
      </Panel>
      {Object.keys(run.data).length > 0 && (
        <Panel title="Trigger data" pad>
          <pre className="code">{JSON.stringify(run.data, null, 2)}</pre>
        </Panel>
      )}
    </>
  )
}
