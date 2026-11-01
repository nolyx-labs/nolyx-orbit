import { useState } from 'react'
import { MetricCharts } from '../../components/charts/MetricCharts'
import { useProject } from '../../components/layout/context'
import { PageHead } from '../../components/ui/PageHead'
import { RangePicker } from '../../components/ui/controls'
import { useApi } from '../../lib/api'
import { useLive } from '../../lib/live'
import { Series, Server } from '../../lib/types'

export function Metrics() {
  const { project } = useProject()
  const [range, setRange] = useState('1h')
  const [agg, setAgg] = useState('avg')
  const [server, setServer] = useState('')
  const [servers] = useApi<Server[]>(`/projects/${project.id}/servers`)
  const path = server ? `/projects/${project.id}/servers/${server}/metrics` : `/projects/${project.id}/metrics`
  const [data, , reload] = useApi<Series>(`${path}?range=${range}&agg=${agg}`)
  useLive(project.id, ['metrics'], reload, 10000)
  return (
    <div className="page">
      <PageHead title="Metrics" sub="Host metrics reported by agents every 10 seconds">
        <select
          className="select"
          style={{ width: 200 }}
          value={server}
          onChange={(e) => setServer(e.target.value)}
          aria-label="Resource"
        >
          <option value="">All servers</option>
          {servers?.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
        <select
          className="select"
          style={{ width: 90 }}
          value={agg}
          onChange={(e) => setAgg(e.target.value)}
          aria-label="Aggregation"
        >
          {['avg', 'min', 'max', 'p95', 'p99'].map((a) => (
            <option key={a}>{a}</option>
          ))}
        </select>
        <RangePicker value={range} onChange={setRange} />
      </PageHead>
      <MetricCharts data={data} aggregate={!server} />
    </div>
  )
}
