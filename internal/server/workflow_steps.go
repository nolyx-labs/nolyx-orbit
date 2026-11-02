// Workflow step types: what each one checks when saved and does when run.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"orbit/internal/protocol"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// stepKind is the seam for step types: add an entry to stepKinds to add one. Tests swap run to avoid agents.
type stepKind struct {
	label    string
	role     string // minimum role the saver needs in the project when the step runs
	canFail  bool   // may carry an on_failure branch
	validate func(st *wfStep) error
	run      func(x *wfExec, ctx context.Context, st wfStep) (output string, err error)
}

var stepKinds map[string]*stepKind

func init() {
	needService := func(st *wfStep) error {
		if st.ServiceID == "" {
			return errStatus(400, "choose a service")
		}
		return nil
	}
	container := func(action string) func(*wfExec, context.Context, wfStep) (string, error) {
		return func(x *wfExec, ctx context.Context, st wfStep) (string, error) {
			return x.containerAction(ctx, st.ServiceID, action)
		}
	}
	stepKinds = map[string]*stepKind{
		"redeploy": {label: "Redeploy", role: "member", canFail: true, validate: needService, run: (*wfExec).redeploy},
		"start":    {label: "Start", role: "member", canFail: true, validate: needService, run: container(protocol.ActionContainerStart)},
		"stop":     {label: "Stop", role: "member", canFail: true, validate: needService, run: container(protocol.ActionContainerStop)},
		"restart":  {label: "Restart", role: "member", canFail: true, validate: needService, run: container(protocol.ActionContainerRestart)},
		"run_command": {label: "Run command in", role: "admin", canFail: true, run: (*wfExec).runCommand, validate: func(st *wfStep) error {
			if st.Command = strings.TrimSpace(st.Command); st.Command == "" || len(st.Command) > 4096 {
				return errStatus(400, "enter a command (up to 4096 characters)")
			}
			if st.Seconds < 0 || st.Seconds > 60 {
				return errStatus(400, "command timeout must be up to 60 seconds")
			}
			return needService(st)
		}},
		"check_monitor": {label: "Check monitor", role: "member", canFail: true, run: (*wfExec).checkMonitor, validate: func(st *wfStep) error {
			if st.MonitorID == "" {
				return errStatus(400, "choose a monitor")
			}
			if st.Seconds < 0 || st.Seconds > 3600 {
				return errStatus(400, "the time limit must be up to 1 hour")
			}
			return nil
		}},
		"http": {label: "HTTP request", role: "member", canFail: true, run: (*wfExec).httpRequest, validate: func(st *wfStep) error {
			if st.Method == "" {
				st.Method = "POST"
			}
			if !contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"}, st.Method) {
				return errStatus(400, "unsupported method %q", st.Method)
			}
			if err := checkHTTPURL(st.URL); err != nil {
				return err
			}
			if len(st.Headers) > 20 || len(st.Body) > 64<<10 {
				return errStatus(400, "too many headers or body too large (64 KB)")
			}
			for k, v := range st.Headers {
				if !headerNameRe.MatchString(k) || strings.ContainsAny(v, "\r\n") {
					return errStatus(400, "invalid header %q", k)
				}
			}
			return nil
		}},
		"alert": {label: "Send alert", role: "member", canFail: true, run: (*wfExec).sendAlert, validate: func(st *wfStep) error {
			if len(st.ChannelIDs)+len(st.UserIDs) == 0 || len(st.ChannelIDs) > 20 || len(st.UserIDs) > 50 {
				return errStatus(400, "choose who gets the alert")
			}
			if len(st.Message) > 2000 {
				return errStatus(400, "the message can be up to 2000 characters")
			}
			return nil
		}},
		"wait": {label: "Wait", role: "viewer", run: wait, validate: func(st *wfStep) error {
			if st.Seconds < 1 || st.Seconds > 86400 {
				return errStatus(400, "wait between 1 second and 24 hours")
			}
			return nil
		}},
		// If is evaluated by the executor; it only needs validation here.
		"if": {label: "If", role: "viewer", validate: func(st *wfStep) error { return checkCond(st.If) }},
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func wait(_ *wfExec, ctx context.Context, st wfStep) (string, error) {
	return fmt.Sprintf("Waited %s", time.Duration(st.Seconds)*time.Second), sleep(ctx, time.Duration(st.Seconds)*time.Second)
}

// serviceContainer finds the service's current container, as appCols does.
func (x *wfExec) serviceContainer(ctx context.Context, serviceID string) (serverID, dockerID, name string, err error) {
	var orch string
	err = x.s.db.QueryRow(ctx, `select a.orchestrator, coalesce(c.server_id::text, ''), coalesce(c.docker_id, ''), a.name from applications a
		left join lateral (select server_id, docker_id from containers c where c.server_id = a.server_id and c.labels->>'orbit.app' = a.name
		  order by created desc limit 1) c on true
		where a.project_id::text = $1 and a.id::text = $2`, x.p.ID, serviceID).Scan(&orch, &serverID, &dockerID, &name)
	if err == pgx.ErrNoRows {
		return "", "", "", errors.New("the service no longer exists")
	} else if err != nil {
		return "", "", "", err
	}
	if orch == "nomad" {
		return "", "", "", errors.New("Nomad services can't be started, stopped or entered from a workflow")
	}
	if dockerID == "" {
		return "", "", "", fmt.Errorf("%s has no container yet: deploy it first", name)
	}
	return serverID, dockerID, name, nil
}

func (x *wfExec) containerAction(ctx context.Context, serviceID, action string) (string, error) {
	serverID, dockerID, name, err := x.serviceContainer(ctx, serviceID)
	if err != nil {
		return "", err
	}
	res, err := x.s.sendCommand(ctx, newUUID(), serverID, x.saverID, action, protocol.ContainerArgs{ContainerID: dockerID}, true)
	if err != nil {
		return "", err
	}
	if !res.OK {
		return "", errors.New(res.Error)
	}
	verb := strings.TrimPrefix(action, "container.")
	x.s.event(ctx, x.p.ID, serverID, "info", fmt.Sprintf("%s: %s by workflow %s", verb, name, x.wfName))
	x.s.hub.publish(x.p.ID, "containers", serverID)
	return fmt.Sprintf("Sent %s to %s", verb, name), nil
}

// redeploy re-runs the service's last deploy, or deploys the pushed commit when a push to its repository started the run,
// then waits for the deploy to finish.
func (x *wfExec) redeploy(ctx context.Context, st wfStep) (string, error) {
	var repo, lastImage string
	if err := x.s.db.QueryRow(ctx, `select a.repo_url, coalesce((select image from deployments where application_id = a.id order by number desc limit 1), '')
		from applications a where a.project_id::text = $1 and a.id::text = $2`, x.p.ID, st.ServiceID).Scan(&repo, &lastImage); err != nil {
		return "", errors.New("the service no longer exists")
	}
	o := deployOpts{Image: lastImage, UserID: x.saverID, Actor: "workflow " + x.wfName + " (" + x.saverEmail + ")"}
	if x.kind == "github_push" && repo != "" && x.data["repo"] == repo {
		str := func(k string) string { s, _ := x.data[k].(string); return s }
		o = deployOpts{CommitSHA: str("commit"), CommitMessage: str("message"), CommitAuthor: str("author"), Ref: str("branch"), UserID: o.UserID, Actor: o.Actor}
	}
	id, number, err := x.s.startDeploy(ctx, x.p, st.ServiceID, o)
	if err != nil {
		return "", err
	}
	for start := time.Now(); time.Since(start) < 30*time.Minute; {
		if err := sleep(ctx, 3*time.Second); err != nil {
			return "", err
		}
		var status, derr string
		if err := x.s.db.QueryRow(ctx, `select status, coalesce(error, '') from deployments where id = $1`, id).Scan(&status, &derr); err != nil {
			return "", err
		}
		switch status {
		case "succeeded":
			return fmt.Sprintf("Deployment #%d succeeded", number), nil
		case "failed":
			return "", fmt.Errorf("deployment #%d failed: %s", number, derr)
		}
	}
	return "", fmt.Errorf("deployment #%d didn't finish within 30 minutes", number)
}

func (x *wfExec) runCommand(ctx context.Context, st wfStep) (string, error) {
	if ok, err := x.s.hasMFA(ctx, x.saverID); err != nil || !ok {
		return "", errors.New("running commands requires the person who saved this workflow to have two-factor authentication turned on")
	}
	serverID, dockerID, name, err := x.serviceContainer(ctx, st.ServiceID)
	if err != nil {
		return "", err
	}
	timeout := st.Seconds
	if timeout == 0 {
		timeout = 30
	}
	// Placeholders are not expanded here: webhook data must never reach a shell.
	res, err := x.s.sendCommand(ctx, newUUID(), serverID, x.saverID, protocol.ActionContainerExec,
		protocol.ContainerArgs{ContainerID: dockerID, Cmd: []string{"sh", "-c", st.Command}, Timeout: timeout}, true)
	if err != nil {
		return "", err
	}
	x.s.event(ctx, x.p.ID, serverID, "info", fmt.Sprintf("Command run in %s by workflow %s", name, x.wfName))
	if !res.OK {
		return "", errors.New(res.Error)
	}
	var out string
	json.Unmarshal(res.Output, &out)
	return out, nil
}

// checkMonitor checks every 10 seconds until the monitor passes or the time limit runs out.
func (x *wfExec) checkMonitor(ctx context.Context, st wfStep) (string, error) {
	m := monitor{ProjectID: x.p.ID}
	if err := x.s.db.QueryRow(ctx, `select id::text, name, type, target, expected_status, keyword, timeout_ms from monitors where project_id::text = $1 and id::text = $2`,
		x.p.ID, st.MonitorID).Scan(&m.ID, &m.Name, &m.Type, &m.Target, &m.ExpectedStatus, &m.Keyword, &m.TimeoutMS); err != nil {
		return "", errors.New("the monitor no longer exists")
	}
	limit := time.Duration(st.Seconds) * time.Second
	if limit == 0 {
		limit = 2 * time.Minute
	}
	start := time.Now()
	for n := 1; ; n++ {
		res := x.s.check(ctx, m)
		x.s.recordCheck(ctx, m, res)
		if res.OK {
			return fmt.Sprintf("%s is up (%d ms, check %d)", m.Name, res.LatencyMS, n), nil
		}
		if time.Since(start)+10*time.Second > limit {
			return "", fmt.Errorf("%s still failing after %d checks: %s", m.Name, n, res.Error)
		}
		if err := sleep(ctx, 10*time.Second); err != nil {
			return "", err
		}
	}
}

func (x *wfExec) httpRequest(ctx context.Context, st wfStep) (string, error) {
	u := x.expand(st.URL)
	if err := checkHTTPURL(u); err != nil {
		return "", errors.New("the URL isn't an http(s) address after filling in placeholders")
	}
	var body io.Reader
	if st.Body != "" {
		body = strings.NewReader(x.expand(st.Body))
	}
	req, err := http.NewRequestWithContext(ctx, st.Method, u, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "NolyxOrbit-Workflow/1")
	if st.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range st.Headers {
		req.Header.Set(k, strings.NewReplacer("\r", "", "\n", "").Replace(x.expand(v)))
	}
	resp, err := x.s.safeClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
	out := fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, b)
	if resp.StatusCode >= 400 {
		return "", errors.New(out)
	}
	return out, nil
}

func (x *wfExec) sendAlert(ctx context.Context, st wfStep) (string, error) {
	msg := x.expand(st.Message)
	if msg == "" {
		msg = "Workflow " + x.wfName + " sent an alert"
	}
	sent, err := x.s.wfNotify(ctx, x.p.ID, false, st.ChannelIDs, st.UserIDs, x.wfName, msg, x.vars["trigger"], x.vars["run_url"])
	if err != nil {
		return "", err
	}
	return "Sent to " + strings.Join(sent, ", "), nil
}

// wfNotify delivers a workflow message to the given channels (or all of the project's) and members' email addresses.
func (s *Server) wfNotify(ctx context.Context, projectID string, allChannels bool, channelIDs, userIDs []string, wfName, msg, trigger, link string) ([]string, error) {
	text := "[Nolyx Orbit] " + msg
	payload := map[string]any{"status": "workflow", "workflow": wfName, "text": text, "content": text, "run_url": link}
	at := time.Now().UTC()
	mail := mailMsg{"alert", map[string]string{"Status": "Workflow", "Color": "#2563eb", "Title": msg, "Rule": "Workflow " + wfName,
		"Target": trigger, "Value": "—", "Time": at.Format("2 Jan 2006, 15:04 UTC"), "Link": link}}
	rows, err := s.db.Query(ctx, `select name, type, config_encrypted from notification_channels
		where project_id::text = $1 and ($3 or id::text = any($2)) order by created_at`, projectID, channelIDs, allChannels)
	if err != nil {
		return nil, err
	}
	type ch struct {
		name, typ string
		cfg       channelConfig
	}
	var chans []ch
	for rows.Next() {
		var c ch
		var enc []byte
		rows.Scan(&c.name, &c.typ, &enc)
		if b, err := s.decrypt(enc); err == nil {
			json.Unmarshal(b, &c.cfg)
			chans = append(chans, c)
		}
	}
	rows.Close()
	var sent, failed []string
	for _, c := range chans {
		if err := s.deliver(ctx, c.typ, c.cfg, text, payload, mail); err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", c.name, err))
		} else {
			sent = append(sent, c.name)
		}
	}
	if len(userIDs) > 0 {
		var to []string
		err := s.db.QueryRow(ctx, `select coalesce(array_agg(u.email), '{}') from users u join memberships m on m.user_id = u.id
			join projects p on p.org_id = m.org_id where p.id::text = $1 and u.id::text = any($2)`, projectID, userIDs).Scan(&to)
		if err != nil {
			return nil, err
		}
		if len(to) > 0 {
			if err := s.sendTemplate(to, text, mail.template, mail.data); err != nil {
				failed = append(failed, fmt.Sprintf("email (%v)", err))
			} else {
				sent = append(sent, strings.Join(to, ", "))
			}
		}
	}
	if len(failed) > 0 {
		return sent, errors.New("could not deliver to " + strings.Join(failed, "; "))
	}
	if len(sent) == 0 {
		return nil, errors.New("no channels or members to deliver to")
	}
	return sent, nil
}
