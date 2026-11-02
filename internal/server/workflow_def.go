// Workflow definitions: the JSON tree, its validation, placeholders, schedules and If conditions.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type wfDef struct {
	Trigger wfTrigger `json:"trigger"`
	Steps   []wfStep  `json:"steps"`
}

// wfTrigger: the scope fields (service, monitor, server, branch, status) narrow an event trigger; empty means any.
type wfTrigger struct {
	Type      string `json:"type"`
	Every     string `json:"every,omitempty"`   // schedule: hourly | daily | weekly
	At        string `json:"at,omitempty"`      // schedule: HH:MM UTC (hourly uses the minutes)
	Weekday   int    `json:"weekday,omitempty"` // schedule weekly: 0 = Sunday
	ServiceID string `json:"service_id,omitempty"`
	MonitorID string `json:"monitor_id,omitempty"`
	ServerID  string `json:"server_id,omitempty"`
	Branch    string `json:"branch,omitempty"` // github_push
	Status    string `json:"status,omitempty"` // deploy_finished: succeeded | failed
}

type wfStep struct {
	Type       string            `json:"type"`
	ServiceID  string            `json:"service_id,omitempty"`
	MonitorID  string            `json:"monitor_id,omitempty"`
	Command    string            `json:"command,omitempty"`
	Seconds    int               `json:"seconds,omitempty"` // wait: how long; check_monitor: time limit; run_command: timeout
	Method     string            `json:"method,omitempty"`
	URL        string            `json:"url,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	ChannelIDs []string          `json:"channel_ids,omitempty"`
	UserIDs    []string          `json:"user_ids,omitempty"`
	Message    string            `json:"message,omitempty"`
	If         *wfCond           `json:"if,omitempty"`
	Then       []wfStep          `json:"then,omitempty"`
	Else       []wfStep          `json:"else,omitempty"`
	OnFailure  []wfStep          `json:"on_failure,omitempty"`
}

type wfCond struct {
	Type      string  `json:"type"` // field_equals | field_contains | monitor_up | service_running | disk_below | time_between
	Field     string  `json:"field,omitempty"`
	Value     string  `json:"value,omitempty"`
	MonitorID string  `json:"monitor_id,omitempty"`
	ServiceID string  `json:"service_id,omitempty"`
	ServerID  string  `json:"server_id,omitempty"`
	Percent   float64 `json:"percent,omitempty"`
	From      string  `json:"from,omitempty"` // HH:MM UTC
	To        string  `json:"to,omitempty"`
}

// triggerScopes lists each trigger type with the scope fields it accepts; the type names double as emit kinds.
var triggerScopes = map[string][]string{
	"manual": nil, "schedule": nil, "webhook": nil,
	"github_push":        {"service_id", "branch"},
	"deploy_finished":    {"service_id", "status"},
	"service_stopped":    {"service_id"},
	"service_restarting": {"service_id"},
	"monitor_down":       {"monitor_id"},
	"monitor_up":         {"monitor_id"},
	"server_offline":     {"server_id"},
	"disk_full":          {"server_id"},
	"backup_failed":      nil,
}

var triggerLabels = map[string]string{
	"manual": "Run by hand", "schedule": "On a schedule", "webhook": "Webhook", "github_push": "Push to GitHub",
	"deploy_finished": "A deploy finished", "service_stopped": "A service stopped", "service_restarting": "A service keeps restarting",
	"monitor_down": "A monitor went down", "monitor_up": "A monitor is back up", "server_offline": "A server stopped reporting",
	"disk_full": "A disk is nearly full", "backup_failed": "A backup failed",
}

func (t *wfTrigger) scope() map[string]*string {
	return map[string]*string{"service_id": &t.ServiceID, "monitor_id": &t.MonitorID, "server_id": &t.ServerID, "branch": &t.Branch, "status": &t.Status}
}

// matches reports whether an event's data falls within the trigger's scope.
func (t wfTrigger) matches(data map[string]any) bool {
	for _, k := range triggerScopes[t.Type] {
		if want := *t.scope()[k]; want != "" && fmt.Sprint(data[k]) != want {
			return false
		}
	}
	return true
}

const (
	wfMaxSteps = 50
	wfMaxDepth = 4
)

var hhmmRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
var headerNameRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// wfRefs collects the ids a definition points at, to check they belong to the project in one query per kind.
type wfRefs struct {
	services, monitors, servers, channels, users map[string]bool
	gitServices                                  map[string]bool
	runsCommands                                 bool
}

// check validates the tree's shape and fields (no database). It normalizes the trigger's scope fields.
func (d *wfDef) check(refs *wfRefs) error {
	t := &d.Trigger
	allowed, ok := triggerScopes[t.Type]
	if !ok {
		return errStatus(400, "unknown trigger %q", t.Type)
	}
	for k, v := range t.scope() {
		if !contains(allowed, k) {
			*v = ""
		}
	}
	if t.Type != "schedule" {
		t.Every, t.At, t.Weekday = "", "", 0
	} else {
		if t.Every != "hourly" && t.Every != "daily" && t.Every != "weekly" {
			return errStatus(400, "choose hourly, daily or weekly")
		}
		if !hhmmRe.MatchString(t.At) {
			return errStatus(400, "enter the schedule time as HH:MM (UTC)")
		}
		if t.Weekday < 0 || t.Weekday > 6 || t.Every != "weekly" && t.Weekday != 0 {
			return errStatus(400, "invalid weekday")
		}
	}
	if t.Status != "" && t.Status != "succeeded" && t.Status != "failed" {
		return errStatus(400, "deploy status must be succeeded or failed")
	}
	if t.Branch != "" && (!pathRe.MatchString(t.Branch) || strings.Contains(t.Branch, "..")) {
		return errStatus(400, "invalid branch")
	}
	if t.Type == "github_push" {
		if t.ServiceID == "" {
			return errStatus(400, "choose the service whose repository to watch")
		}
		refs.gitServices[t.ServiceID] = true
	}
	refs.add(t.ServiceID, t.MonitorID, t.ServerID)
	if len(d.Steps) == 0 {
		return errStatus(400, "add at least one step")
	}
	n := 0
	return checkSteps(d.Steps, 1, &n, refs)
}

func (r *wfRefs) add(service, monitor, server string) {
	for m, v := range map[*map[string]bool]string{&r.services: service, &r.monitors: monitor, &r.servers: server} {
		if v != "" {
			(*m)[v] = true
		}
	}
}

func checkSteps(steps []wfStep, depth int, n *int, refs *wfRefs) error {
	if depth > wfMaxDepth {
		return errStatus(400, "steps can be nested at most %d levels deep", wfMaxDepth)
	}
	for i := range steps {
		st := &steps[i]
		if *n++; *n > wfMaxSteps {
			return errStatus(400, "a workflow can have at most %d steps", wfMaxSteps)
		}
		k := stepKinds[st.Type]
		if k == nil {
			return errStatus(400, "unknown step %q", st.Type)
		}
		if err := k.validate(st); err != nil {
			return err
		}
		if !k.canFail && len(st.OnFailure) > 0 {
			return errStatus(400, "%s steps can't fail, so they have no failure branch", k.label)
		}
		if st.Type != "if" && (len(st.Then) > 0 || len(st.Else) > 0) {
			return errStatus(400, "only If steps have branches")
		}
		refs.add(st.ServiceID, st.MonitorID, "")
		if st.If != nil {
			refs.add(st.If.ServiceID, st.If.MonitorID, st.If.ServerID)
		}
		for _, c := range st.ChannelIDs {
			refs.channels[c] = true
		}
		for _, u := range st.UserIDs {
			refs.users[u] = true
		}
		refs.runsCommands = refs.runsCommands || st.Type == "run_command"
		for _, b := range [][]wfStep{st.Then, st.Else, st.OnFailure} {
			if err := checkSteps(b, depth+1, n, refs); err != nil {
				return err
			}
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// validateWorkflow checks the tree and that everything it references belongs to the project.
func (s *Server) validateWorkflow(ctx context.Context, p project, saverID string, d *wfDef) error {
	refs := &wfRefs{services: map[string]bool{}, monitors: map[string]bool{}, servers: map[string]bool{},
		channels: map[string]bool{}, users: map[string]bool{}, gitServices: map[string]bool{}}
	if err := d.check(refs); err != nil {
		return err
	}
	for _, c := range []struct {
		ids  map[string]bool
		sql  string
		what string
	}{
		{refs.services, `select count(*) from applications where project_id::text = $1 and id::text = any($2)`, "service"},
		{refs.gitServices, `select count(*) from applications where project_id::text = $1 and id::text = any($2) and repo_url <> ''`, "service connected to a Git repository"},
		{refs.monitors, `select count(*) from monitors where project_id::text = $1 and id::text = any($2)`, "monitor"},
		{refs.servers, `select count(*) from servers where project_id::text = $1 and id::text = any($2)`, "server"},
		{refs.channels, `select count(*) from notification_channels where project_id::text = $1 and id::text = any($2)`, "notification channel"},
		{refs.users, `select count(*) from memberships m join projects p on p.org_id = m.org_id where p.id::text = $1 and m.user_id::text = any($2)`, "member"},
	} {
		if len(c.ids) == 0 {
			continue
		}
		ids := make([]string, 0, len(c.ids))
		for id := range c.ids {
			ids = append(ids, id)
		}
		var n int
		if err := s.db.QueryRow(ctx, c.sql, p.ID, ids).Scan(&n); err != nil {
			return err
		}
		if n != len(ids) {
			return errStatus(400, "a step or trigger points to a %s that isn't in this project", c.what)
		}
	}
	if refs.runsCommands {
		if ok, err := s.hasMFA(ctx, saverID); err != nil {
			return err
		} else if !ok {
			return errStatus(400, "running commands in containers requires two-factor authentication: turn it on in Settings → Account")
		}
	}
	return nil
}

func (s *Server) hasMFA(ctx context.Context, userID string) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `select totp_enabled_at is not null from users where id::text = $1`, userID).Scan(&ok)
	return ok, err
}

func checkHTTPURL(u string) error {
	p, err := url.Parse(u)
	// The host may be a placeholder, so only the scheme is checked here; safeClient guards the destination at run time.
	if err != nil || (p.Scheme != "http" && p.Scheme != "https") {
		return errStatus(400, "enter a URL starting with http:// or https://")
	}
	return nil
}

var placeholderRe = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}`)

// expand substitutes {{name}} and {{data.a.b}} once, left to right; substituted text is never re-scanned or evaluated.
// Missing data fields become empty; unknown names are left as written so typos are visible.
func expand(s string, vars map[string]string, data map[string]any) string {
	return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := lookup(placeholderRe.FindStringSubmatch(m)[1], vars, data); ok {
			return v
		}
		return m
	})
}

func lookup(name string, vars map[string]string, data map[string]any) (string, bool) {
	path, isData := strings.CutPrefix(name, "data.")
	if !isData {
		v, ok := vars[name]
		return v, ok
	}
	var cur any = data
	for _, k := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", true
		}
		cur = m[k]
	}
	switch v := cur.(type) {
	case nil:
		return "", true
	case string:
		return v, true
	default:
		b, _ := json.Marshal(v)
		return string(b), true
	}
}

func hhmm(s string) int {
	h, _ := strconv.Atoi(s[:2])
	m, _ := strconv.Atoi(s[3:])
	return h*60 + m
}

// scheduleSlot is the latest scheduled time at or before now. A run is due when that slot is after the last one run.
func scheduleSlot(t wfTrigger, now time.Time) time.Time {
	now = now.UTC()
	at := hhmm(t.At)
	switch t.Every {
	case "hourly":
		slot := now.Truncate(time.Hour).Add(time.Duration(at%60) * time.Minute)
		if slot.After(now) {
			slot = slot.Add(-time.Hour)
		}
		return slot
	case "daily", "weekly":
		slot := time.Date(now.Year(), now.Month(), now.Day(), at/60, at%60, 0, 0, time.UTC)
		back, period := 0, 1
		if t.Every == "weekly" {
			back, period = (int(now.Weekday())-t.Weekday+7)%7, 7
		}
		slot = slot.AddDate(0, 0, -back)
		if slot.After(now) {
			slot = slot.AddDate(0, 0, -period)
		}
		return slot
	}
	return time.Time{}
}

// timeBetween: from inclusive, to exclusive, wrapping past midnight when from > to.
func timeBetween(from, to string, now time.Time) bool {
	n, f, t := now.UTC().Hour()*60+now.UTC().Minute(), hhmm(from), hhmm(to)
	if f <= t {
		return n >= f && n < t
	}
	return n >= f || n < t
}

func checkCond(c *wfCond) error {
	if c == nil {
		return errStatus(400, "an If step needs a condition")
	}
	switch c.Type {
	case "field_equals", "field_contains":
		if c.Field == "" || len(c.Field) > 200 || len(c.Value) > 1000 {
			return errStatus(400, "choose a trigger field to compare")
		}
	case "monitor_up":
		if c.MonitorID == "" {
			return errStatus(400, "choose a monitor")
		}
	case "service_running":
		if c.ServiceID == "" {
			return errStatus(400, "choose a service")
		}
	case "disk_below":
		if c.ServerID == "" || c.Percent <= 0 || c.Percent > 100 {
			return errStatus(400, "choose a server and a percentage between 1 and 100")
		}
	case "time_between":
		if !hhmmRe.MatchString(c.From) || !hhmmRe.MatchString(c.To) {
			return errStatus(400, "enter times as HH:MM (UTC)")
		}
	default:
		return errStatus(400, "unknown condition %q", c.Type)
	}
	return nil
}

// fieldCond evaluates the comparisons that only look at the trigger's data.
func fieldCond(c wfCond, vars map[string]string, data map[string]any) bool {
	v, _ := lookup(c.Field, vars, data)
	if c.Type == "field_equals" {
		return v == c.Value
	}
	return strings.Contains(v, c.Value)
}
