import { Link } from 'react-router'
import { useProject } from '../../components/layout/context'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Copy } from '../../components/ui/controls'
import { Loading } from '../../components/ui/states'
import { Badge } from '../../components/ui/status'
import { useApi } from '../../lib/api'

export function AIApps() {
  const { project } = useProject()
  const [tools] =
    useApi<{ name: string; description: string; access: 'read' | 'change' | 'destructive' }[]>('/mcp/tools')
  const url = `${location.origin}/mcp`
  const clients = [
    ['Claude Code', `claude mcp add --transport http orbit ${url} --header "Authorization: Bearer orb_key_…"`],
    [
      'Cursor (~/.cursor/mcp.json)',
      JSON.stringify({ mcpServers: { orbit: { url, headers: { Authorization: 'Bearer orb_key_…' } } } }, null, 2),
    ],
    [
      'VS Code with GitHub Copilot (.vscode/mcp.json)',
      JSON.stringify(
        { servers: { orbit: { type: 'http', url, headers: { Authorization: 'Bearer orb_key_…' } } } },
        null,
        2,
      ),
    ],
  ]
  return (
    <div className="page">
      <PageHead
        title="AI apps"
        sub="Run your infrastructure from the AI app you already use, through the Model Context Protocol"
      />
      <div className="stack">
        <Panel title="Connect an app">
          <div className="panel-body stack">
            <div>
              1. Create an <Link to={`/p/${project.id}/api-keys`}>API key</Link>. Its role and project limit what the AI
              app can do: a <b>viewer</b> key can only read. 2. Add Orbit to your app with the key, using one of the
              snippets below. Any app that supports remote MCP servers over HTTP with a custom header works.
            </div>
            {clients.map(([name, snippet]) => (
              <div key={name} className="field">
                <span>{name}</span>
                <div className="copy-row">
                  <pre className="code">{snippet}</pre>
                  <Copy text={snippet} />
                </div>
              </div>
            ))}
            <div className="hint">
              Every action is checked against the key's role and recorded in the audit log as the key. Actions that
              can't be undone are marked destructive: the app has to ask you before running them.
            </div>
          </div>
        </Panel>
        <Panel title={`Tools${tools ? ` · ${tools.length}` : ''}`}>
          {!tools ? (
            <Loading />
          ) : (
            <table className="t">
              <tbody>
                {tools.map((t) => (
                  <tr key={t.name}>
                    <td className="mono" style={{ whiteSpace: 'nowrap' }}>
                      {t.name}
                    </td>
                    <td className="muted">{t.description}</td>
                    <td className="right">
                      <Badge tone={t.access === 'destructive' ? 'err' : t.access === 'change' ? 'warn' : ''}>
                        {t.access}
                      </Badge>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      </div>
    </div>
  )
}
