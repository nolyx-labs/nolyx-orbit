import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { ListenPort, RefreshInventory, useInsight } from './insight'

const reach = (p: ListenPort) =>
  p.scope === 'all' ? (
    <StatusText tone="warn">All interfaces</StatusText>
  ) : p.scope === 'localhost' ? (
    <StatusText tone="ok">This server only</StatusText>
  ) : (
    <StatusText tone="">This address only</StatusText>
  )

export function ServerPorts({ sid }: { sid: string }) {
  const [ins, error, reload] = useInsight(sid)
  if (error) return <Empty title={error} />
  if (!ins) return <Loading />
  const ports = ins.inventory?.ports ?? []
  return (
    <Panel
      title="Listening ports"
      actions={<RefreshInventory sid={sid} at={ins.inventory_at} onDone={reload} />}
    >
      {!ins.inventory ? (
        <Empty title="Waiting for the agent's first report" />
      ) : !ports.length ? (
        <Empty title="Nothing is listening" />
      ) : (
        <div className="table-wrap">
          <table className="t">
            <thead>
              <tr>
                <th>Port</th>
                <th>Address</th>
                <th>Reachable on</th>
                <th>Process</th>
                <th>Container</th>
              </tr>
            </thead>
            <tbody>
              {ports.map((p) => (
                <tr key={`${p.proto}/${p.address}/${p.port}`}>
                  <td className="mono">
                    {p.port}/{p.proto}
                  </td>
                  <td className="mono muted">{p.address}</td>
                  <td>{reach(p)}</td>
                  <td>
                    {p.process ? (
                      <>
                        <span className="mono">{p.process}</span> <span className="faint">pid {p.pid}</span>
                      </>
                    ) : (
                      <span className="faint">unknown</span>
                    )}
                  </td>
                  <td className="mono">{p.container || <span className="faint">—</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="panel-body faint">
        "All interfaces" means other machines can connect unless a firewall blocks it. The Security tab checks which
        ones are actually open.
      </div>
    </Panel>
  )
}
