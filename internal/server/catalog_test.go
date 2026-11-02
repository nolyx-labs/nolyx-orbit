package server

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"orbit/internal/protocol"
)

var imageRefRe = regexp.MustCompile(`^[a-z0-9.-]+(/[a-z0-9._-]+)*:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// refs lists the ${kind:key} references in an app's env and command (and its companions' wiring).
func refs(kind string, vals ...map[string]string) []string {
	var out []string
	for _, m := range vals {
		for _, v := range m {
			for _, r := range catalogRef.FindAllStringSubmatch(v, -1) {
				if r[1] == kind {
					out = append(out, r[2])
				}
			}
		}
	}
	return out
}

func cmdMap(cmd []string) map[string]string {
	m := map[string]string{}
	for i, c := range cmd {
		m[string(rune('a'+i))] = c
	}
	return m
}

func defaultPorts(a catalogApp) []int {
	ps := make([]int, len(a.Ports))
	for i, p := range a.Ports {
		ps[i] = p.Host
	}
	return ps
}

func requiredInputs(a catalogApp) map[string]string {
	in := map[string]string{}
	for _, i := range a.Inputs {
		switch {
		case i.Type == "checkbox" && i.Required:
			in[i.Key] = "true"
		case i.Required && i.Default == "" && i.Type == "email":
			in[i.Key] = "admin@example.com"
		case i.Required && i.Default == "":
			in[i.Key] = "https://app.example.com"
		}
	}
	return in
}

func TestCatalogEntriesAreValid(t *testing.T) {
	cats := map[string]bool{}
	for _, c := range catalogCategories {
		cats[c.ID] = true
	}
	ids := map[string]bool{}
	for _, a := range catalogApps {
		t.Run(a.ID, func(t *testing.T) {
			if ids[a.ID] || !appNameRe.MatchString(a.ID) {
				t.Fatalf("duplicate or invalid id")
			}
			ids[a.ID] = true
			if !cats[a.Category] || a.Name == "" || a.Description == "" || !strings.HasPrefix(a.Website, "https://") {
				t.Errorf("missing category, name, description or https website")
			}
			if a.Link != "" && a.Image == "" {
				return // a pointer to another part of Orbit
			}
			if !imageRefRe.MatchString(a.Image) || strings.HasSuffix(a.Image, ":latest") {
				t.Errorf("image %q needs an explicit, pinned tag", a.Image)
			}
			seenPorts := map[string]bool{}
			for _, p := range a.Ports {
				_, ctr, err := protocol.ParsePort("1:" + p.Container)
				if err != nil || seenPorts[ctr] || p.Host < 0 || p.Host > 65535 || p.Label == "" {
					t.Errorf("bad port %+v", p)
				}
				seenPorts[ctr] = true
			}
			if err := (appInput{Volumes: a.Volumes}).validate(); err != nil {
				t.Errorf("volumes: %v", err)
			}
			if a.Health != "" && (!strings.HasPrefix(a.Health, "/") || len(a.Ports) == 0) {
				t.Errorf("health check %q needs a leading / and a port", a.Health)
			}
			for k := range a.Env {
				if !envKeyRe.MatchString(k) {
					t.Errorf("invalid env name %q", k)
				}
			}

			// Companions: exist, can be companions (an image, no prompts, no companions of their own: no cycles).
			allEnv := []map[string]string{a.Env, cmdMap(a.Command)}
			secrets := map[string]bool{}
			for _, s := range a.Secrets {
				if secrets[s.Key] || s.Label == "" {
					t.Errorf("duplicate or unlabeled secret %q", s.Key)
				}
				secrets[s.Key] = true
			}
			suffixes := map[string]bool{}
			for _, c := range a.Companions {
				base, ok := findCatalogApp(c.App)
				if !ok || base.Image == "" || len(base.Companions) > 0 || len(base.Inputs) > 0 {
					t.Errorf("companion %q must be a catalog app with an image, no inputs and no companions", c.App)
				}
				if suffixes[c.Suffix] || !appNameRe.MatchString(c.Suffix) || c.Label == "" {
					t.Errorf("bad companion suffix %q", c.Suffix)
				}
				suffixes[c.Suffix] = true
				for k := range c.Env {
					if !envKeyRe.MatchString(k) {
						t.Errorf("invalid env name %q", k)
					}
				}
				for _, s := range base.Secrets {
					if secrets[s.Key] {
						t.Errorf("secret %q is declared by both the app and companion %s", s.Key, c.App)
					}
					secrets[s.Key] = true
				}
				allEnv = append(allEnv, c.Env, c.ParentEnv, base.Env, cmdMap(base.Command))
			}
			// Only required companions may be referenced outside their own wiring.
			for _, s := range refs("service", a.Env, cmdMap(a.Command)) {
				i := slices.IndexFunc(a.Companions, func(c catalogCompanion) bool { return c.Suffix == s })
				if s != "" && (i < 0 || a.Companions[i].Optional) {
					t.Errorf("${service:%s} doesn't name a required companion", s)
				}
			}
			for _, s := range refs("service", allEnv...) {
				if s != "" && !suffixes[s] {
					t.Errorf("${service:%s} doesn't name a companion", s)
				}
			}

			// Secrets and inputs: everything referenced is declared, everything declared is used.
			used := map[string]bool{}
			for _, s := range refs("secret", allEnv...) {
				used[s] = true
				if !secrets[s] {
					t.Errorf("${secret:%s} isn't declared", s)
				}
			}
			for _, s := range a.Secrets {
				if !used[s.Key] {
					t.Errorf("secret %q is never used", s.Key)
				}
			}
			usedIn := map[string]bool{}
			for _, s := range refs("input", allEnv...) {
				usedIn[s] = true
			}
			for _, i := range a.Inputs {
				if !usedIn[i.Key] || !slices.Contains([]string{"text", "email", "url", "checkbox"}, i.Type) || i.Label == "" {
					t.Errorf("input %q is unused, unlabeled or has an unknown type", i.Key)
				}
				delete(usedIn, i.Key)
			}
			for k := range usedIn {
				t.Errorf("${input:%s} isn't declared", k)
			}

			if a.Link != "" {
				return
			}
			all := map[string]bool{}
			for _, c := range a.Companions {
				all[c.Suffix] = true
			}
			svcs, _, err := planInstall(a, a.ID, "10.0.0.5", defaultPorts(a), requiredInputs(a), all)
			if err != nil {
				t.Fatalf("install with defaults: %v", err)
			}
			for _, sv := range svcs {
				for k, v := range sv.Env {
					if strings.Contains(v, "${") || strings.Contains(v, "{url}") || strings.Contains(v, "{host}") {
						t.Errorf("%s: %s=%q is not fully resolved", sv.Name, k, v)
					}
				}
			}
		})
	}
	for _, want := range []string{"postgresql", "mysql", "wordpress", "nextcloud", "minecraft", "own-app"} {
		if !ids[want] {
			t.Errorf("catalog lacks %s", want)
		}
	}
}

func TestPlanInstallWiresCompanions(t *testing.T) {
	wp, _ := findCatalogApp("wordpress")
	svcs, creds, err := planInstall(wp, "blog", "10.0.0.5", []int{8080}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 2 || svcs[0].Name != "blog-db" || svcs[1].Name != "blog" {
		t.Fatalf("want companion first, got %+v", svcs)
	}
	db, app := svcs[0], svcs[1]
	if app.Env["WORDPRESS_DB_HOST"] != "blog-db" || app.Env["WORDPRESS_DB_PASSWORD"] != db.Env["MARIADB_PASSWORD"] ||
		len(db.Env["MARIADB_PASSWORD"]) != 32 || db.Env["MARIADB_ROOT_PASSWORD"] == db.Env["MARIADB_PASSWORD"] {
		t.Errorf("database wiring: app %v, db %v", app.Env, db.Env)
	}
	if len(db.Ports) != 0 || db.HealthCmd == "" || !slices.Equal(db.Volumes, []string{"data:/var/lib/mysql"}) {
		t.Errorf("companion: %+v", db)
	}
	if !slices.Equal(app.Ports, []string{"8080:80"}) || app.Health == "" {
		t.Errorf("app ports/health: %+v", app)
	}
	if len(creds) != 2 || creds[0].Service != "blog" || creds[0].Value != app.Env["WORDPRESS_DB_PASSWORD"] ||
		creds[1].Service != "blog-db" || creds[1].Value != db.Env["MARIADB_ROOT_PASSWORD"] {
		t.Errorf("credentials: %+v", creds)
	}
	again, _, _ := planInstall(wp, "blog", "", []int{8080}, nil, nil)
	if again[1].Env["WORDPRESS_DB_PASSWORD"] == app.Env["WORDPRESS_DB_PASSWORD"] {
		t.Error("secrets repeat across installs")
	}
	// No published first port: no HTTP health check.
	if s, _, _ := planInstall(wp, "blog", "", []int{0}, nil, nil); s[1].Health != "" || len(s[1].Ports) != 0 {
		t.Errorf("unpublished port kept health check: %+v", s[1])
	}

	n8n, _ := findCatalogApp("n8n")
	s, _, _ := planInstall(n8n, "flows", "", []int{5678}, nil, nil)
	if len(s) != 1 || s[0].Env["DB_TYPE"] != "" {
		t.Errorf("optional companion installed by default: %+v", s)
	}
	s, _, _ = planInstall(n8n, "flows", "", []int{5678}, nil, map[string]bool{"db": true})
	if len(s) != 2 || s[1].Env["DB_POSTGRESDB_HOST"] != "flows-db" || s[1].Env["DB_POSTGRESDB_PASSWORD"] != s[0].Env["POSTGRES_PASSWORD"] {
		t.Errorf("optional companion wiring: %+v", s)
	}
	if _, _, err := planInstall(n8n, "flows", "", []int{5678}, nil, map[string]bool{"nope": true}); err == nil {
		t.Error("unknown companion accepted")
	}
}

func TestPlanInstallInputsAndPorts(t *testing.T) {
	mc, _ := findCatalogApp("minecraft")
	if _, _, err := planInstall(mc, "mc", "", []int{25565}, nil, nil); err == nil {
		t.Error("EULA not required")
	}
	s, _, err := planInstall(mc, "mc", "", []int{25565}, map[string]string{"eula": "true"}, nil)
	if err != nil || s[0].Env["EULA"] != "TRUE" || s[0].Env["MEMORY"] != "2G" {
		t.Errorf("eula/defaults: %v %v", err, s)
	}
	for name, in := range map[string]map[string]string{
		"unknown input": {"eula": "true", "x": "1"},
		"control chars": {"eula": "true", "motd": "a\nb"},
		"bad checkbox":  {"eula": "yes"},
	} {
		if _, _, err := planInstall(mc, "mc", "", []int{25565}, in, nil); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	ghost, _ := findCatalogApp("ghost")
	s, _, _ = planInstall(ghost, "blog", "10.0.0.5", []int{2368}, nil, nil)
	if s[1].Env["url"] != "http://10.0.0.5:2368" {
		t.Errorf("url default = %q", s[1].Env["url"])
	}
	if _, _, err := planInstall(ghost, "blog", "", []int{2368}, map[string]string{"url": "javascript:alert(1)"}, nil); err == nil {
		t.Error("non-http URL accepted")
	}
	st, _ := findCatalogApp("syncthing")
	if _, _, err := planInstall(st, "sync", "", []int{8384, 22000, 22000, 0}, nil, nil); err != nil {
		t.Errorf("same port number on tcp and udp rejected: %v", err)
	}
	for name, ports := range map[string][]int{"duplicate": {8384, 8384, 22000, 0}, "count": {8384}, "range": {70000, 1, 2, 3}} {
		if _, _, err := planInstall(st, "sync", "", ports, nil, nil); err == nil {
			t.Errorf("%s ports accepted", name)
		}
	}
	if _, _, err := planInstall(st, "Bad Name", "", []int{8384, 22000, 22000, 0}, nil, nil); err == nil {
		t.Error("invalid name accepted")
	}
}

func TestGenSecret(t *testing.T) {
	a, b := genSecret(0), genSecret(64)
	if len(a) != 32 || len(b) != 64 || a == b[:32] || !regexp.MustCompile(`^[A-Z2-7]+$`).MatchString(a+b) {
		t.Errorf("genSecret: %q %q", a, b)
	}
}
