import { Panel } from '../../../components/ui/Panel'
import { Empty } from '../../../components/ui/states'
import { Database } from '../../../lib/types'

export function DBQueries({ d }: { d: Database }) {
  const qs = d.stats.slow_queries ?? []
  return (
    <Panel
      title="Active queries running longer than 1s"
      actions={
        <span className="faint" style={{ fontSize: 12 }}>
          sampled every 15s
        </span>
      }
    >
      {!qs.length ? (
        <Empty title="No slow queries right now" />
      ) : (
        <table className="t">
          <thead>
            <tr>
              <th>PID</th>
              <th className="right">Running</th>
              <th>Waiting on</th>
              <th>Query</th>
            </tr>
          </thead>
          <tbody>
            {qs.map((q) => (
              <tr key={q.pid}>
                <td className="mono">{q.pid}</td>
                <td className="right mono">{q.seconds.toFixed(1)}s</td>
                <td className="muted">{q.wait || '—'}</td>
                <td className="mono" style={{ fontSize: 11, maxWidth: 700, whiteSpace: 'pre-wrap' }}>
                  {q.query}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}
