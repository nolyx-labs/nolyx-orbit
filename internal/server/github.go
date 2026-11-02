// GitHub: commit lookup, build remotes and push webhooks.
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var githubRepoRe = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(\.git)?/?$`)

func gitRemote(repo, token string) string {
	u, _ := url.Parse(strings.TrimSuffix(repo, "/"))
	if !strings.HasSuffix(u.Path, ".git") {
		u.Path += ".git" // Docker only treats the URL as a git context with a .git suffix
	}
	if token != "" {
		u.User = url.UserPassword("x-access-token", token)
	}
	return u.String()
}

var githubAPI = "https://api.github.com"

// resolveCommit looks up the branch head on GitHub; other hosts build the ref as given.
func resolveCommit(ctx context.Context, repo, ref, token string) (sha, msg, author string, err error) {
	m := githubRepoRe.FindStringSubmatch(repo)
	if m == nil {
		return "", "", "", nil
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", githubAPI+"/repos/"+m[1]+"/"+m[2]+"/commits/"+url.PathEscape(ref), nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", "", fmt.Errorf("github: %d", resp.StatusCode)
	}
	var c struct {
		SHA    string
		Commit struct {
			Message string
			Author  struct{ Name string }
		}
	}
	err = json.NewDecoder(resp.Body).Decode(&c)
	return c.SHA, strings.SplitN(c.Commit.Message, "\n", 2)[0], c.Commit.Author.Name, err
}

// githubWebhook handles push events: verify the per-app HMAC secret, then deploy the pushed commit.
func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		return err
	}
	var secret, projectID, orgID, appName, branch string
	var auto bool
	err = s.db.QueryRow(r.Context(), `select a.webhook_secret, a.project_id::text, p.org_id::text, a.name, a.branch, a.auto_deploy
		from applications a join projects p on p.id = a.project_id where a.id::text = $1 and a.repo_url <> ''`, r.PathValue("aid")).
		Scan(&secret, &projectID, &orgID, &appName, &branch, &auto)
	if err != nil {
		return errStatus(404, "not found")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	if !hmac.Equal([]byte("sha256="+hex.EncodeToString(mac.Sum(nil))), []byte(r.Header.Get("X-Hub-Signature-256"))) {
		return errStatus(401, "invalid signature")
	}
	if r.Header.Get("X-GitHub-Event") != "push" {
		writeJSON(w, 200, map[string]string{"status": "ignored"})
		return nil
	}
	var push struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		Deleted    bool   `json:"deleted"`
		HeadCommit *struct {
			Message string `json:"message"`
			Author  struct {
				Name string `json:"name"`
			} `json:"author"`
		} `json:"head_commit"`
		Pusher struct {
			Name string `json:"name"`
		} `json:"pusher"`
	}
	if err := json.Unmarshal(body, &push); err != nil {
		return errStatus(400, "invalid payload")
	}
	s.emitPush(r.Context(), projectID, r.PathValue("aid"), appName, body)
	if branch == "" {
		branch = "main"
	}
	if !auto || push.Deleted || push.Ref != "refs/heads/"+branch || push.HeadCommit == nil {
		writeJSON(w, 200, map[string]string{"status": "ignored"})
		return nil
	}
	p := project{ID: projectID, OrgID: orgID}
	actor := "github:" + push.Pusher.Name
	s.audit(r, orgID, projectID, "", actor, "app.deploy", appName, map[string]any{"commit": push.After, "source": "github"})
	id, number, err := s.startDeploy(r.Context(), p, r.PathValue("aid"), deployOpts{
		CommitSHA: push.After, CommitMessage: strings.SplitN(push.HeadCommit.Message, "\n", 2)[0], CommitAuthor: push.HeadCommit.Author.Name,
		Ref: branch, Actor: actor})
	if err != nil {
		return err
	}
	writeJSON(w, 202, map[string]any{"deployment_id": id, "number": number})
	return nil
}
