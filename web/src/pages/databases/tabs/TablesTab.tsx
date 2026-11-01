import { Panel } from '../../../components/ui/Panel'
import { Empty } from '../../../components/ui/states'
import { bytes } from '../../../lib/format'
import { Database } from '../../../lib/types'

export function DBTables({ d }: { d: Database }) {
  const ts = d.stats.tables ?? []
  const max = Math.max(1, ...ts.map((t) => t.bytes))
  return (
    <Panel title="Largest tables">
      {!ts.length ? (
        <Empty title="No user tables" />
      ) : (
        <table className="t">
          <thead>
            <tr>
              <th>Table</th>
              <th>Size</th>
              <th className="right">Rows (est.)</th>
            </tr>
          </thead>
          <tbody>
            {ts.map((t) => (
              <tr key={t.name}>
                <td className="mono">{t.name}</td>
                <td>
                  <div className="meter">
                    <div className="bar">
                      <i style={{ width: `${(t.bytes / max) * 100}%` }} />
                    </div>
                    <span className="val" style={{ width: 70 }}>
                      {bytes(t.bytes)}
                    </span>
                  </div>
                </td>
                <td className="right mono">{t.rows.toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}
