import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router'
import { Shell } from './components/layout/Shell'
import { Auth } from './pages/auth/Auth'
import { InviteAccept } from './pages/auth/InviteAccept'
import { ResetPassword } from './pages/auth/ResetPassword'
import { Backups } from './pages/databases/Backups'
import { DatabaseDetail } from './pages/databases/DatabaseDetail'
import { Databases } from './pages/databases/Databases'
import { Replicas } from './pages/databases/Replicas'
import { DeploymentDetail } from './pages/deployments/DeploymentDetail'
import { Deployments } from './pages/deployments/Deployments'
import { Releases } from './pages/deployments/Releases'
import { Docker } from './pages/docker/Docker'
import { Clusters } from './pages/nomad/Clusters'
import { NomadPage } from './pages/nomad/NomadPage'
import { Events } from './pages/observability/Events'
import { Logs } from './pages/observability/Logs'
import { Metrics } from './pages/observability/Metrics'
import { Alerts } from './pages/observability/alerts/Alerts'
import { MonitorDetail } from './pages/monitors/MonitorDetail'
import { Monitors } from './pages/monitors/Monitors'
import { OperationPage } from './pages/operations/OperationPage'
import { Overview } from './pages/overview/Overview'
import { Access } from './pages/security/Access'
import { ApiKeys } from './pages/security/ApiKeys'
import { Secrets } from './pages/security/Secrets'
import { ServerDetail } from './pages/servers/ServerDetail'
import { Servers } from './pages/servers/Servers'
import { Catalog } from './pages/services/Catalog'
import { NewService } from './pages/services/NewService'
import { ServiceDetail } from './pages/services/ServiceDetail'
import { Services } from './pages/services/Services'
import { AIApps } from './pages/settings/AIApps'
import { Account } from './pages/settings/Account'
import { Agents } from './pages/settings/Agents'
import { StatusPageDetail } from './pages/status/StatusPageDetail'
import { StatusPages } from './pages/status/StatusPages'
import { OrganizationSettings } from './pages/settings/OrganizationSettings'
import { ProjectsSettings } from './pages/settings/ProjectsSettings'
import { Updates } from './pages/updates/Updates'
import { WorkflowDetail } from './pages/workflows/WorkflowDetail'
import { Workflows } from './pages/workflows/Workflows'
import './styles.css'

try {
  document.documentElement.dataset.theme = localStorage.getItem('orbit-theme') ?? 'dark'
} catch {
  document.documentElement.dataset.theme = 'dark'
}
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<Auth mode="login" />} />
        <Route path="/signup" element={<Auth mode="signup" />} />
        <Route path="/invite/:token" element={<InviteAccept />} />
        <Route path="/forgot-password" element={<ResetPassword />} />
        <Route path="/reset-password/:token" element={<ResetPassword />} />
        <Route path="/p/:pid" element={<Shell />}>
          <Route index element={<Navigate to="overview" replace />} />
          <Route path="overview" element={<Overview />} />
          <Route path="servers" element={<Servers />} />
          <Route path="servers/:sid" element={<Navigate to="overview" replace />} />
          <Route path="servers/:sid/:tab" element={<ServerDetail />} />
          <Route path="docker" element={<Docker />} />
          <Route path="services" element={<Services />} />
          <Route path="services/new" element={<NewService />} />
          <Route path="catalog" element={<Catalog />} />
          <Route path="services/:aid" element={<ServiceDetail />} />
          <Route path="deployments" element={<Deployments />} />
          <Route path="deployments/:did" element={<DeploymentDetail />} />
          <Route path="metrics" element={<Metrics />} />
          <Route path="events" element={<Events />} />
          <Route path="alerts" element={<Alerts />} />
          <Route path="monitors" element={<Monitors />} />
          <Route path="monitors/:id" element={<MonitorDetail />} />
          <Route path="status-pages" element={<StatusPages />} />
          <Route path="status-pages/:id" element={<Navigate to="incidents" replace />} />
          <Route path="status-pages/:id/:tab" element={<StatusPageDetail />} />
          <Route path="updates" element={<Updates />} />
          <Route path="workflows" element={<Workflows />} />
          <Route path="workflows/:id" element={<WorkflowDetail />} />
          <Route path="workflows/:id/runs/:rid" element={<WorkflowDetail />} />
          <Route path="workflows/:id/:tab" element={<WorkflowDetail />} />
          <Route path="access" element={<Access />} />
          <Route path="settings/agents" element={<Agents />} />
          <Route path="clusters" element={<Clusters />} />
          <Route path="nomad" element={<NomadPage />} />
          <Route path="releases" element={<Releases />} />
          <Route path="databases" element={<Databases />} />
          <Route path="databases/:id" element={<DatabaseDetail />} />
          <Route path="databases/:id/:tab" element={<DatabaseDetail />} />
          <Route path="replicas" element={<Replicas />} />
          <Route path="backups" element={<Backups />} />
          <Route path="operations/:oid" element={<OperationPage />} />
          <Route path="logs" element={<Logs />} />
          <Route path="secrets" element={<Secrets />} />
          <Route path="api-keys" element={<ApiKeys />} />
          <Route path="settings/organization" element={<OrganizationSettings />} />
          <Route path="settings/projects" element={<ProjectsSettings />} />
          <Route path="settings/account" element={<Account />} />
          <Route path="settings/ai-apps" element={<AIApps />} />
        </Route>
        <Route path="*" element={<Shell />} />
      </Routes>
    </BrowserRouter>
  </React.StrictMode>,
)
