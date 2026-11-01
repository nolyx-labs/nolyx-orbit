import { Meter } from '../../components/ui/Meter'
import { Panel } from '../../components/ui/Panel'
import { bitrate, bytes, duration, ratio } from '../../lib/format'
import { Server } from '../../lib/types'

export function ServerOverview({ s }: { s: Server }) {
  const on = s.status === 'online'
  return (
    <div className="split">
      <Panel title="Usage">
        <div className="panel-body">
          <div className="kv">
            <div className="k">CPU</div>
            <div>
              <Meter value={on ? s.cpu : null} />
            </div>
            <div className="k">Memory</div>
            <div className="row">
              <Meter value={on ? ratio(s.mem_used, s.mem_total) : null} />
              <span className="mono faint">
                {bytes(s.mem_used)} / {bytes(s.mem_total)}
              </span>
            </div>
            <div className="k">Disk</div>
            <div className="row">
              <Meter value={ratio(s.disk_used, s.disk_total)} />
              <span className="mono faint">
                {bytes(s.disk_used)} / {bytes(s.disk_total)}
              </span>
            </div>
            <div className="k">Network</div>
            <div className="mono">
              {on ? (
                <>
                  ↓ {bitrate(s.net_rx)} &nbsp; ↑ {bitrate(s.net_tx)}
                </>
              ) : (
                '—'
              )}
            </div>
            <div className="k">Load (1m)</div>
            <div className="mono">{s.load1?.toFixed(2) ?? '—'}</div>
            <div className="k">Disk I/O</div>
            <div className="mono">
              {on ? (
                <>
                  r {bytes(s.io_read)}/s &nbsp; w {bytes(s.io_write)}/s &nbsp; {s.io_util?.toFixed(0) ?? '—'}% busy
                </>
              ) : (
                '—'
              )}
            </div>
            <div className="k">Containers</div>
            <div className="mono">{s.containers_running} running</div>
          </div>
        </div>
      </Panel>
      <Panel title="System">
        <div className="panel-body">
          <div className="kv">
            <div className="k">OS</div>
            <div>{s.os}</div>
            <div className="k">Architecture</div>
            <div className="mono">{s.arch}</div>
            <div className="k">Kernel</div>
            <div className="mono">{s.kernel}</div>
            <div className="k">CPU</div>
            <div className="mono">{s.cpu_count} cores</div>
            <div className="k">Uptime</div>
            <div className="mono">{duration(s.uptime)}</div>
            <div className="k">Agent</div>
            <div className="mono">{s.agent_version}</div>
            <div className="k">Docker</div>
            <div className="mono">{s.docker_version || 'not detected'}</div>
            <div className="k">Nomad</div>
            <div className="mono">{s.nomad_version || <span className="faint">not running</span>}</div>
            <div className="k">Region</div>
            <div>{s.region || <span className="faint">not set</span>}</div>
          </div>
        </div>
      </Panel>
    </div>
  )
}
