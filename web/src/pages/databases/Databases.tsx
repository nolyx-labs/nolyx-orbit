import { useState } from 'react'
import { useCan, useProject } from '../../components/layout/context'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { useApi } from '../../lib/api'
import { useLive } from '../../lib/live'
import { Database } from '../../lib/types'
import { ConnectDialog } from './ConnectDialog'
import { DBTable } from './DatabaseTable'
import { clusters } from './dbHelpers'
import { ProvisionDialog } from './ProvisionDialog'
import { Topology } from './Topology'

export function Databases() {
  const { project } = useProject()
  const canAdmin = useCan('admin')
  const [dbs, , reload] = useApi<Database[]>(`/projects/${project.id}/databases`)
  const [dialog, setDialog] = useState<'connect' | 'provision' | null>(null)
  useLive(project.id, ['databases'], reload)
  return (
    <div className="page">
      <PageHead title="Databases" sub="PostgreSQL clusters, replicas and backups, monitored by your agents">
        {canAdmin && (
          <button className="btn" onClick={() => setDialog('connect')}>
            Connect existing
          </button>
        )}
        {canAdmin && (
          <button className="btn primary" onClick={() => setDialog('provision')}>
            Create PostgreSQL
          </button>
        )}
      </PageHead>
      {!dbs ? (
        <Loading />
      ) : !dbs.length ? (
        <Panel>
          <Empty title="No databases">
            Connect an existing PostgreSQL that an agent can reach, or let Orbit run one on a server.
          </Empty>
        </Panel>
      ) : (
        <div className="stack">
          {clusters(dbs).map((c, i) => (
            <Panel
              key={i}
              title={c.primary?.name ?? 'Cluster'}
              actions={
                c.primary && (
                  <span className="faint mono" style={{ fontSize: 12 }}>
                    PostgreSQL {c.primary.version.split(' ')[0]}
                  </span>
                )
              }
            >
              <div className="panel-body">
                <Topology c={c} />
              </div>
              <DBTable dbs={[...(c.primary ? [c.primary] : []), ...c.extraPrimaries, ...c.replicas]} />
            </Panel>
          ))}
        </div>
      )}
      {dialog === 'connect' && <ConnectDialog onClose={() => setDialog(null)} onDone={reload} />}
      {dialog === 'provision' && <ProvisionDialog onClose={() => setDialog(null)} />}
    </div>
  )
}
