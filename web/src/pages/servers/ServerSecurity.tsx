import { Fragment, useState } from 'react'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Panel } from '../../components/ui/Panel'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { Badge, StatusText, Tone } from '../../components/ui/status'
import { api } from '../../lib/api'
import { ago } from '../../lib/format'
import { FirewallRule, Insight, useInsight } from './insight'

const severityTone: Record<string, Tone> = { high: 'err', medium: 'warn', low: 'info' }

export function ServerSecurity({ sid }: { sid: string }) {
  const { project } = useProject()
  const canScan = useCan('member')
  const [ins, loadError, reload] = useInsight(sid)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  if (loadError) return <Empty title={loadError} />
  if (!ins) return <Loading />
  const scan = ins.scan
  const scanNow = () => {
    setBusy(true)
    setError(undefined)
    api('POST', `/projects/${project.id}/servers/${sid}/scan`)
      .then(reload, (e) => setError(e.message))
      .finally(() => setBusy(false))
  }
  return (
    <div className="stack">
      <Panel
        title="Security scan"
        actions={
          <span className="row" style={{ gap: 10 }}>
            <span className="faint">{scan ? `Scanned ${ago(ins.scan_at)}` : 'Not scanned yet'}</span>
            {canScan && (
              <button className="btn sm" disabled={busy} onClick={scanNow}>
                {busy ? 'Scanning…' : 'Scan'}
              </button>
            )}
          </span>
        }
      >
        <ErrorNote error={error} />
        {scan?.container && (
          <div className="callout warn" style={{ margin: 14 }}>
            The agent runs in a container, so the firewall, SSH settings and pending updates couldn't be checked.
            Install it with install.sh for a full scan.
          </div>
        )}
        {!scan ? (
          <Empty title="The agent scans after it connects and once a day" />
        ) : !scan.findings.length ? (
          <Empty title="No issues found" />
        ) : (
          <div className="list">
            {scan.findings.map((f) => (
              <div key={f.key} className="list-item" style={{ alignItems: 'flex-start' }}>
                <span style={{ width: 70 }}>
                  <Badge tone={severityTone[f.severity]}>{f.severity}</Badge>
                </span>
                <div style={{ flex: 1 }}>
                  <div style={{ fontWeight: 500 }}>{f.title}</div>
                  <div className="muted">{f.detail}</div>
                  <div className="mono faint" style={{ marginTop: 4 }}>
                    {f.fix}
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
        {scan && <Checks scan={scan} />}
      </Panel>
      {scan && !scan.container && <Firewall sid={sid} scan={scan} onChanged={reload} />}
    </div>
  )
}

function Checks({ scan }: { scan: NonNullable<Insight['scan']> }) {
  return (
    <div className="panel-body kv">
      <div className="k">SSH root login</div>
      <div className="mono">{scan.ssh.permit_root_login || '—'}</div>
      <div className="k">SSH passwords</div>
      <div className="mono">{scan.ssh.password_authentication || '—'}</div>
      <div className="k">Automatic updates</div>
      <div>{scan.auto_updates === 'unknown' ? '—' : scan.auto_updates || 'off'}</div>
      <div className="k">Security updates</div>
      <div>
        {scan.security_updates < 0 ? '—' : `${scan.security_updates} waiting`}{' '}
        <span className="faint">(a quick count from the package cache)</span>
      </div>
      <div className="k">Restart needed</div>
      <div>{scan.reboot_required ? 'yes' : 'no'}</div>
      {scan.errors?.map((e) => (
        <Fragment key={e}>
          <div className="k">Couldn't check</div>
          <div className="faint">{e}</div>
        </Fragment>
      ))}
    </div>
  )
}

/** Close is only offered for single-port rules Orbit can remove exactly, never for the SSH port. */
const closable = (r: FirewallRule, sshPorts: number[]) =>
  /^\d+$/.test(r.port) &&
  (r.proto === 'tcp' || r.proto === 'udp') &&
  !r.v6 &&
  r.action.startsWith('ALLOW') &&
  !(r.proto === 'tcp' && sshPorts.includes(Number(r.port)))

function Firewall({ sid, scan, onChanged }: { sid: string; scan: NonNullable<Insight['scan']>; onChanged: () => void }) {
  const { project } = useProject()
  const canAdmin = useCan('admin')
  const [f, setF] = useState({ port: '', proto: 'tcp', source: '' })
  const [error, setError] = useState<string>()
  const [closing, setClosing] = useState<FirewallRule>()
  const fw = scan.firewall
  const sshPorts = scan.ssh.ports?.length ? scan.ssh.ports : [22]
  const change = (body: object) => api('POST', `/projects/${project.id}/servers/${sid}/firewall`, body).then(onChanged)
  if (!fw.tool)
    return (
      <Panel title="Firewall" pad>
        <div style={{ fontWeight: 500, marginBottom: 4 }}>No firewall tool is installed</div>
        <div className="muted">
          Install ufw and allow SSH before turning it on, or you can lock yourself out:
          <div className="mono" style={{ marginTop: 6 }}>
            apt install ufw && ufw allow OpenSSH && ufw enable
          </div>
          On RHEL, Rocky or Fedora: <span className="mono">dnf install firewalld && systemctl enable --now firewalld</span>
        </div>
      </Panel>
    )
  return (
    <Panel
      title="Firewall"
      actions={
        <StatusText tone={fw.active ? 'ok' : 'warn'}>
          {fw.tool} {fw.active ? 'active' : 'off'}
        </StatusText>
      }
    >
      <div className="panel-body kv">
        <div className="k">Incoming by default</div>
        <div>{fw.incoming || '—'}</div>
        <div className="k">Outgoing by default</div>
        <div>{fw.outgoing || '—'}</div>
      </div>
      {!fw.active && (
        <div className="callout warn" style={{ margin: '0 14px 12px' }}>
          The firewall is off, so rules have no effect until it's turned on. Orbit doesn't turn it on for you, since a
          missing SSH rule would lock you out.
        </div>
      )}
      {!fw.rules?.length ? (
        <Empty title="No rules" />
      ) : (
        <table className="t">
          <thead>
            <tr>
              <th>Port</th>
              <th>Action</th>
              <th>From</th>
              {canAdmin && <th />}
            </tr>
          </thead>
          <tbody>
            {fw.rules.map((r, i) => (
              <tr key={i}>
                <td className="mono">
                  {r.port || '—'}
                  {r.proto && `/${r.proto}`} {r.v6 && <span className="faint">IPv6</span>}
                </td>
                <td>{r.action}</td>
                <td className="mono muted">{r.from}</td>
                {canAdmin && (
                  <td className="right">
                    {closable(r, sshPorts) && (
                      <button className="btn sm danger" onClick={() => setClosing(r)}>
                        Close
                      </button>
                    )}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {canAdmin && (
        <div className="panel-body row" style={{ flexWrap: 'wrap' }}>
          <label className="field" style={{ width: 110 }}>
            <span>Port</span>
            <input
              className="input mono"
              inputMode="numeric"
              value={f.port}
              onChange={(e) => setF({ ...f, port: e.target.value })}
            />
          </label>
          <label className="field" style={{ width: 100 }}>
            <span>Protocol</span>
            <select className="select" value={f.proto} onChange={(e) => setF({ ...f, proto: e.target.value })}>
              <option>tcp</option>
              <option>udp</option>
            </select>
          </label>
          <label className="field" style={{ width: 220 }}>
            <span>From (optional)</span>
            <input
              className="input mono"
              placeholder="anywhere, or 203.0.113.0/24"
              value={f.source}
              onChange={(e) => setF({ ...f, source: e.target.value })}
            />
          </label>
          <button
            className="btn"
            style={{ alignSelf: 'flex-end' }}
            disabled={!f.port}
            onClick={() => {
              setError(undefined)
              change({ action: 'open', port: Number(f.port), proto: f.proto, source: f.source.trim() }).then(
                () => setF({ ...f, port: '', source: '' }),
                (e) => setError(e.message),
              )
            }}
          >
            Open port
          </button>
          <ErrorNote error={error} />
        </div>
      )}
      {closing && (
        <Confirm
          title={`Close port ${closing.port}/${closing.proto}?`}
          action="Close port"
          danger
          onClose={() => setClosing(undefined)}
          onConfirm={() =>
            change({
              action: 'close',
              port: Number(closing.port),
              proto: closing.proto,
              source: closing.from === 'Anywhere' ? '' : closing.from,
            })
          }
        >
          New connections to this port from {closing.from === 'Anywhere' ? 'anywhere' : closing.from} will be blocked.
          Ports Docker publishes stay reachable, because Docker bypasses the firewall.
        </Confirm>
      )}
    </Panel>
  )
}
