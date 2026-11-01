import { useState } from 'react'
import { Chart } from '../../../components/charts/Chart'
import { useProject } from '../../../components/layout/context'
import { RangePicker } from '../../../components/ui/controls'
import { Loading } from '../../../components/ui/states'
import { useApi } from '../../../lib/api'
import { bytes, num } from '../../../lib/format'
import { useLive } from '../../../lib/live'
import { Database, Series } from '../../../lib/types'
import { lag } from '../dbHelpers'

export function DBOverview({ d }: { d: Database }) {
  const { project } = useProject()
  const [range, setRange] = useState('1h')
  const [m, , reload] = useApi<Series>(`/projects/${project.id}/database-metrics?database=${d.id}&range=${range}`)
  useLive(project.id, ['databases'], reload, 15000)
  const st = d.stats
  return (
    <div className="stack" style={{ gap: 12 }}>
      <div className="cards">
        <div className="card">
          <div className="k">Connections</div>
          <div className="v">
            {st.connections ?? '—'}
            <small>/ {st.max_connections ?? '—'}</small>
          </div>
          <div className="s">{st.waiting_locks ? `${st.waiting_locks} waiting on locks` : 'No lock waits'}</div>
        </div>
        <div className="card">
          <div className="k">Transactions/sec</div>
          <div className="v">{num(st.tps)}</div>
          <div className="s">{st.qps != null ? `${num(st.qps)} queries/sec` : 'QPS needs pg_stat_statements'}</div>
        </div>
        <div className="card">
          <div className="k">Cache hit ratio</div>
          <div className="v">{st.cache_hit == null ? '—' : `${st.cache_hit.toFixed(2)}%`}</div>
          <div className="s">
            {st.cache_hit != null && st.cache_hit < 95 ? 'Below 95%: consider more shared_buffers' : 'Healthy'}
          </div>
        </div>
        <div className="card">
          <div className="k">Database size</div>
          <div className="v">{bytes(st.size)}</div>
          <div className="s mono">WAL {st.wal_position || '—'}</div>
        </div>
      </div>
      <div className="row">
        <div className="spacer" />
        <RangePicker value={range} onChange={setRange} />
      </div>
      {m ? (
        <div className="charts">
          <Chart
            title="Connections"
            t={m.t}
            series={[{ label: 'Connections', values: m.connections, color: 'series-1' }]}
            format={(v) => v.toFixed(0)}
            integer
          />
          <Chart
            title="Transactions / sec"
            t={m.t}
            series={[{ label: 'TPS', values: m.tps, color: 'series-1' }]}
            format={(v) => v.toFixed(1)}
          />
          <Chart
            title="Queries / sec"
            t={m.t}
            series={[{ label: 'QPS', values: m.qps, color: 'series-1' }]}
            format={(v) => v.toFixed(1)}
          />
          <Chart
            title="Cache hit ratio"
            t={m.t}
            series={[{ label: 'Cache hit', values: m.cache_hit, color: 'series-1' }]}
            format={(v) => `${v.toFixed(1)}%`}
            percent
          />
          {d.role === 'replica' && (
            <Chart
              title="Replication lag"
              t={m.t}
              series={[{ label: 'Lag', values: m.lag_ms, color: 'series-1' }]}
              format={(v) => lag(v)}
            />
          )}
          {d.role === 'replica' && (
            <Chart
              title="Replay rate"
              t={m.t}
              series={[{ label: 'Replay', values: m.replay_rate, color: 'series-1' }]}
              format={(v) => `${bytes(v)}/s`}
            />
          )}
          <Chart
            title="Database size"
            t={m.t}
            series={[{ label: 'Size', values: m.size, color: 'series-1' }]}
            format={(v) => bytes(v)}
          />
          <Chart
            title="Lock waits"
            t={m.t}
            series={[{ label: 'Waiting', values: m.waiting_locks, color: 'series-1' }]}
            format={(v) => v.toFixed(0)}
            integer
          />
        </div>
      ) : (
        <Loading />
      )}
    </div>
  )
}
