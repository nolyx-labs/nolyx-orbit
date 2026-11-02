import { Fragment } from 'react'
import { Meter } from '../../components/ui/Meter'
import { Panel } from '../../components/ui/Panel'
import { bytes, ratio } from '../../lib/format'
import { useInsight } from './insight'

/** Hardware details the agent reports with its inventory; the System panel covers the rest. */
export function ServerSpecs({ sid }: { sid: string }) {
  const [ins] = useInsight(sid)
  const specs = ins?.inventory?.specs
  if (!specs) return null
  return (
    <div className="split" style={{ marginTop: 16 }}>
      <Panel title="Filesystems">
        <table className="t">
          <tbody>
            {specs.disks?.map((d) => (
              <tr key={d.device}>
                <td>
                  <div className="name mono">{d.mount}</div>
                  <div className="sub mono">
                    {d.device} · {d.fs}
                  </div>
                </td>
                <td style={{ width: 200 }}>
                  <Meter value={ratio(d.used, d.size)} />
                </td>
                <td className="right mono faint nowrap">
                  {bytes(d.used)} / {bytes(d.size)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Panel>
      <Panel title="Hardware and network">
        <div className="panel-body kv">
          <div className="k">CPU model</div>
          <div>{specs.cpu_model || '—'}</div>
          {specs.interfaces?.map((i) => (
            <Fragment key={i.name}>
              <div className="k mono">{i.name}</div>
              <div className="mono">
                {i.addrs.join(', ') || <span className="faint">no address</span>}
                {!i.up && <span className="faint"> · down</span>}
              </div>
            </Fragment>
          ))}
        </div>
      </Panel>
    </div>
  )
}
