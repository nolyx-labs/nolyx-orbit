// The MCP tool catalogue. Each tool is an API route; path parameters come from arguments of the same name,
// the rest go in the query string (GET/DELETE) or the JSON body.
package server

import "strings"

const (
	accessRead        = "read"
	accessChange      = "change"
	accessDestructive = "destructive" // can't be undone: needs confirm=true, which the model must get from the user
)

type mcpParam struct {
	Name, Type, Description string
	Required                bool
	Enum                    []string
	As                      string // the API's field name, when it differs (e.g. confirm_name → confirm)
}

type mcpTool struct {
	Name, Description, Method, Path, Access string
	Params                                  []mcpParam // besides path parameters
}

func (t mcpTool) pathParams() []string {
	var out []string
	for _, seg := range strings.Split(t.Path, "/") {
		if strings.HasPrefix(seg, "{") {
			out = append(out, strings.Trim(seg, "{}"))
		}
	}
	return out
}

var pathParamHelp = map[string]string{
	"project_id": "Project ID, from list_projects", "org_id": "Organization ID, from list_projects",
	"server_id": "Server ID, from list_servers", "service_id": "Service ID, from list_services",
	"monitor_id": "Monitor ID, from list_monitors", "page_id": "Status page ID, from list_status_pages",
	"incident_id": "Incident ID, from get_status_page", "database_id": "Database ID, from list_databases",
	"backup_id": "Backup ID, from list_database_backups", "deployment_id": "Deployment ID, from list_deployments",
	"container_id": "Docker container ID, from list_containers", "alert_id": "Alert incident ID, from list_alerts",
	"component_id": "Component ID, from get_status_page",
}

func (t mcpTool) describe() map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, p := range t.pathParams() {
		props[p] = map[string]any{"type": "string", "description": pathParamHelp[p]}
		required = append(required, p)
	}
	for _, p := range t.Params {
		s := map[string]any{"type": p.Type, "description": p.Description}
		if p.Enum != nil {
			s["enum"] = p.Enum
		}
		props[p.Name] = s
		if p.Required {
			required = append(required, p.Name)
		}
	}
	if t.Access == accessDestructive {
		props["confirm"] = map[string]any{"type": "boolean", "description": "Set to true only after the user explicitly approved this action"}
		required = append(required, "confirm")
	}
	return map[string]any{
		"name": t.Name, "description": t.Description,
		"inputSchema": map[string]any{"type": "object", "properties": props, "required": required},
		"annotations": map[string]any{"readOnlyHint": t.Access == accessRead, "destructiveHint": t.Access == accessDestructive},
	}
}

var (
	rangeParam = mcpParam{Name: "range", Type: "string", Description: "Time range", Enum: []string{"5m", "15m", "1h", "6h", "24h", "7d", "30d"}}
	impacts    = []string{"none", "minor", "major", "critical"}
)

var mcpTools = []mcpTool{
	// Workspace
	{Name: "list_projects", Description: "List the organizations and projects this key can use, with your role in each.", Method: "GET", Path: "/me", Access: accessRead},
	{Name: "get_overview", Description: "Summary of a project: servers, services, databases, recent problems.", Method: "GET", Path: "/projects/{project_id}/overview", Access: accessRead},
	{Name: "list_events", Description: "Recent infrastructure events (deploys, outages, alerts) in a project.", Method: "GET", Path: "/projects/{project_id}/events", Access: accessRead,
		Params: []mcpParam{{Name: "server", Type: "string", Description: "Only events for this server ID"}}},
	{Name: "get_audit_log", Description: "Who changed what in an organization (admin role).", Method: "GET", Path: "/orgs/{org_id}/audit", Access: accessRead},

	// Servers and containers
	{Name: "list_servers", Description: "List servers with status, region and resource use.", Method: "GET", Path: "/projects/{project_id}/servers", Access: accessRead},
	{Name: "get_server", Description: "One server: host details, status and agent version.", Method: "GET", Path: "/projects/{project_id}/servers/{server_id}", Access: accessRead},
	{Name: "get_server_metrics", Description: "CPU, memory, disk, network and load over time for a server.", Method: "GET", Path: "/projects/{project_id}/servers/{server_id}/metrics", Access: accessRead,
		Params: []mcpParam{rangeParam}},
	{Name: "list_containers", Description: "Docker containers across the project's servers, with state and health.", Method: "GET", Path: "/projects/{project_id}/containers", Access: accessRead},
	{Name: "get_container_logs", Description: "Recent log lines of a container.", Method: "GET", Path: "/projects/{project_id}/servers/{server_id}/containers/{container_id}/logs", Access: accessRead,
		Params: []mcpParam{{Name: "tail", Type: "integer", Description: "Number of lines (default 200)"}}},
	{Name: "start_container", Description: "Start a stopped container.", Method: "POST", Path: "/projects/{project_id}/servers/{server_id}/containers/{container_id}/start", Access: accessChange},
	{Name: "restart_container", Description: "Restart a container.", Method: "POST", Path: "/projects/{project_id}/servers/{server_id}/containers/{container_id}/restart", Access: accessChange},
	{Name: "stop_container", Description: "Stop a container. Its service is down until started again.", Method: "POST", Path: "/projects/{project_id}/servers/{server_id}/containers/{container_id}/stop", Access: accessDestructive},

	// Logs
	{Name: "search_logs", Description: "Search central logs across servers.", Method: "GET", Path: "/projects/{project_id}/logs", Access: accessRead,
		Params: []mcpParam{{Name: "q", Type: "string", Description: "Text to search for"}, rangeParam,
			{Name: "level", Type: "string", Description: "Comma-separated levels, e.g. error,warn"},
			{Name: "server", Type: "string", Description: "Server ID"}, {Name: "app", Type: "string", Description: "Service name"},
			{Name: "container", Type: "string", Description: "Container name"}}},

	// Services and deploys
	{Name: "list_services", Description: "List services (deployed apps) with status and last deployment.", Method: "GET", Path: "/projects/{project_id}/apps", Access: accessRead},
	{Name: "get_service", Description: "One service: image or repository, ports, health check, current container.", Method: "GET", Path: "/projects/{project_id}/apps/{service_id}", Access: accessRead},
	{Name: "deploy_service", Description: "Deploy a service again, optionally with a different image tag.", Method: "POST", Path: "/projects/{project_id}/apps/{service_id}/deploy", Access: accessChange,
		Params: []mcpParam{{Name: "image", Type: "string", Description: "Image to deploy instead of the configured one"}}},
	{Name: "list_deployments", Description: "Recent deployments with status.", Method: "GET", Path: "/projects/{project_id}/deployments", Access: accessRead},
	{Name: "get_deployment", Description: "One deployment with its step log.", Method: "GET", Path: "/projects/{project_id}/deployments/{deployment_id}", Access: accessRead},
	{Name: "list_releases", Description: "Kept releases per service, for rollbacks.", Method: "GET", Path: "/projects/{project_id}/releases", Access: accessRead},

	// Databases
	{Name: "list_databases", Description: "PostgreSQL databases with role, status and replication.", Method: "GET", Path: "/projects/{project_id}/databases", Access: accessRead},
	{Name: "get_database", Description: "One database with live stats, slow queries and issues.", Method: "GET", Path: "/projects/{project_id}/databases/{database_id}", Access: accessRead},
	{Name: "list_database_backups", Description: "Database backups with size and status.", Method: "GET", Path: "/projects/{project_id}/backups", Access: accessRead},
	{Name: "backup_database", Description: "Back up a database now.", Method: "POST", Path: "/projects/{project_id}/databases/{database_id}/backups", Access: accessChange},
	{Name: "restore_database_backup", Description: "Replace a database's contents with a backup. Data written since the backup is lost.", Method: "POST",
		Path: "/projects/{project_id}/databases/{database_id}/backups/{backup_id}/restore", Access: accessDestructive,
		Params: []mcpParam{{Name: "confirm_name", Type: "string", Description: "The database's name, typed back", Required: true, As: "confirm"}}},

	// Monitors and alerts
	{Name: "list_monitors", Description: "Uptime monitors with status, response time and uptime.", Method: "GET", Path: "/projects/{project_id}/monitors", Access: accessRead},
	{Name: "get_monitor", Description: "One monitor with uptime, response times and outages.", Method: "GET", Path: "/projects/{project_id}/monitors/{monitor_id}", Access: accessRead,
		Params: []mcpParam{rangeParam}},
	{Name: "create_monitor", Description: "Add an HTTP or TCP uptime monitor.", Method: "POST", Path: "/projects/{project_id}/monitors", Access: accessChange,
		Params: []mcpParam{{Name: "name", Type: "string", Required: true, Description: "Display name"},
			{Name: "type", Type: "string", Required: true, Enum: []string{"http", "tcp"}, Description: "http checks a URL, tcp checks host:port"},
			{Name: "target", Type: "string", Required: true, Description: "URL, or host:port for tcp"},
			{Name: "interval_seconds", Type: "integer", Description: "30–3600, default 60"},
			{Name: "expected_status", Type: "string", Description: "http only, e.g. 2xx or 200-299,301"},
			{Name: "keyword", Type: "string", Description: "http only: the response must contain this"}}},
	{Name: "check_monitor_now", Description: "Run a monitor's check immediately and return the result.", Method: "POST", Path: "/projects/{project_id}/monitors/{monitor_id}/check", Access: accessChange},
	{Name: "delete_monitor", Description: "Delete a monitor and its history.", Method: "DELETE", Path: "/projects/{project_id}/monitors/{monitor_id}", Access: accessDestructive},
	{Name: "list_alerts", Description: "Alert rules, open and recent alert incidents, and notification channels.", Method: "GET", Path: "/projects/{project_id}/alerts", Access: accessRead},
	{Name: "acknowledge_alert", Description: "Acknowledge an open alert incident.", Method: "POST", Path: "/projects/{project_id}/incidents/{alert_id}/ack", Access: accessChange},

	// Status pages
	{Name: "list_status_pages", Description: "Status pages with their overall status.", Method: "GET", Path: "/projects/{project_id}/status-pages", Access: accessRead},
	{Name: "get_status_page", Description: "A status page's components, incidents and maintenance.", Method: "GET", Path: "/projects/{project_id}/status-pages/{page_id}", Access: accessRead},
	{Name: "post_status_incident", Description: "Post an incident or schedule maintenance on a status page.", Method: "POST", Path: "/projects/{project_id}/status-pages/{page_id}/incidents", Access: accessChange,
		Params: []mcpParam{{Name: "kind", Type: "string", Required: true, Enum: []string{"incident", "maintenance"}, Description: "incident or maintenance"},
			{Name: "name", Type: "string", Required: true, Description: "Title"},
			{Name: "status", Type: "string", Required: true, Description: "investigating|identified|monitoring|resolved, or scheduled|in_progress|verifying|completed for maintenance"},
			{Name: "impact", Type: "string", Enum: impacts, Description: "Incidents only"},
			{Name: "message", Type: "string", Required: true, Description: "First update shown to visitors"},
			{Name: "components", Type: "object", Description: "Component ID → status while open (operational, degraded_performance, partial_outage, major_outage, under_maintenance)"},
			{Name: "scheduled_for", Type: "string", Description: "Maintenance start, RFC 3339"}, {Name: "scheduled_until", Type: "string", Description: "Maintenance end, RFC 3339"},
			{Name: "notify", Type: "boolean", Description: "Notify subscribers"}}},
	{Name: "update_status_incident", Description: "Post an update to an incident or maintenance, optionally changing its status.", Method: "POST",
		Path: "/projects/{project_id}/status-pages/{page_id}/incidents/{incident_id}/updates", Access: accessChange,
		Params: []mcpParam{{Name: "status", Type: "string", Required: true, Description: "New status"},
			{Name: "message", Type: "string", Required: true, Description: "Update text shown to visitors"},
			{Name: "components", Type: "object", Description: "Component ID → status while open"},
			{Name: "notify", Type: "boolean", Description: "Notify subscribers"}}},
}

var mcpToolByName = func() map[string]mcpTool {
	m := map[string]mcpTool{}
	for _, t := range mcpTools {
		m[t.Name] = t
	}
	return m
}()
