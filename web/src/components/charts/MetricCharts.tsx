import { bitrate, bytes } from '../../lib/format'
import { Series } from '../../lib/types'
import { Chart } from './Chart'

const fmtPct = (v: number) => `${v.toFixed(1)}%`

export function MetricCharts({ data, aggregate }: { data?: Series; aggregate?: boolean }) {
  if (!data)
    return (
      <div className="charts">
        {[0, 1, 2, 3, 4, 5].map((i) => (
          <div key={i} className="chart-panel">
            <div className="chart-empty">Loading…</div>
          </div>
        ))}
      </div>
    )
  const avg = aggregate ? ' (avg across servers)' : ''
  return (
    <div className="charts">
      <Chart
        title={`CPU usage${avg}`}
        t={data.t}
        series={[{ label: 'CPU', values: data.cpu, color: 'series-1' }]}
        format={fmtPct}
        percent
      />
      <Chart
        title={`Memory usage${avg}`}
        t={data.t}
        series={[{ label: 'Memory', values: data.mem, color: 'series-1' }]}
        format={fmtPct}
        percent
      />
      <Chart
        title={aggregate ? 'Network traffic (all servers)' : 'Network traffic'}
        t={data.t}
        series={[
          { label: 'In', values: data.rx, color: 'series-1' },
          { label: 'Out', values: data.tx, color: 'series-2' },
        ]}
        format={bitrate}
      />
      <Chart
        title={`Disk usage${avg}`}
        t={data.t}
        series={[{ label: 'Disk', values: data.disk, color: 'series-1' }]}
        format={fmtPct}
        percent
      />
      <Chart
        title={aggregate ? 'Disk I/O (all servers)' : 'Disk I/O'}
        t={data.t}
        series={[
          { label: 'Read', values: data.io_read, color: 'series-1' },
          { label: 'Write', values: data.io_write, color: 'series-2' },
        ]}
        format={(v) => `${bytes(v)}/s`}
      />
      <Chart
        title={aggregate ? 'Disk busy (busiest server)' : 'Disk busy'}
        t={data.t}
        series={[{ label: 'Busy', values: data.io_util, color: 'series-1' }]}
        format={fmtPct}
        percent
      />
    </div>
  )
}
