import { useState } from 'react'
import { MetricCharts } from '../../components/charts/MetricCharts'
import { useProject } from '../../components/layout/context'
import { RangePicker } from '../../components/ui/controls'
import { useApi } from '../../lib/api'
import { useLive } from '../../lib/live'
import { Series } from '../../lib/types'

export function ServerMetrics({ sid }: { sid: string }) {
  const { project } = useProject()
  const [range, setRange] = useState('1h')
  const [agg, setAgg] = useState('avg')
  const [data, , reload] = useApi<Series>(`/projects/${project.id}/servers/${sid}/metrics?range=${range}&agg=${agg}`)
  useLive(project.id, ['metrics'], reload, 10000)
  return (
    <div className="stack" style={{ gap: 12 }}>
      <div className="row">
        <div className="spacer" />
        <select
          className="select"
          style={{ width: 100 }}
          value={agg}
          onChange={(e) => setAgg(e.target.value)}
          aria-label="Aggregation"
        >
          {['avg', 'min', 'max', 'p95', 'p99'].map((a) => (
            <option key={a}>{a}</option>
          ))}
        </select>
        <RangePicker value={range} onChange={setRange} />
      </div>
      <MetricCharts data={data} />
    </div>
  )
}
