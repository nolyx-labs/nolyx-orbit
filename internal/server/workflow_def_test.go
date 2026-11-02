package server

import (
	"strings"
	"testing"
	"time"
)

func newRefs() *wfRefs {
	return &wfRefs{services: map[string]bool{}, monitors: map[string]bool{}, servers: map[string]bool{},
		channels: map[string]bool{}, users: map[string]bool{}, gitServices: map[string]bool{}}
}

func TestWorkflowCheck(t *testing.T) {
	ok := wfDef{Trigger: wfTrigger{Type: "deploy_finished", ServiceID: "a1", MonitorID: "stray"}, Steps: []wfStep{
		{Type: "if", If: &wfCond{Type: "field_equals", Field: "data.status", Value: "succeeded"},
			Then: []wfStep{{Type: "http", URL: "https://hooks.example.com/{{service}}"}},
			Else: []wfStep{{Type: "alert", ChannelIDs: []string{"c1"}}}},
		{Type: "restart", ServiceID: "a2", OnFailure: []wfStep{{Type: "alert", UserIDs: []string{"u1"}}}},
		{Type: "check_monitor", MonitorID: "m1"},
		{Type: "run_command", ServiceID: "a2", Command: " ls "},
	}}
	refs := newRefs()
	if err := ok.check(refs); err != nil {
		t.Fatal(err)
	}
	if ok.Trigger.MonitorID != "" {
		t.Error("scope fields that don't apply to the trigger should be cleared")
	}
	if ok.Steps[0].Then[0].Method != "POST" || ok.Steps[3].Command != "ls" {
		t.Error("defaults and trimming not applied")
	}
	if !refs.services["a1"] || !refs.services["a2"] || !refs.monitors["m1"] || !refs.channels["c1"] || !refs.users["u1"] || !refs.runsCommands {
		t.Errorf("refs not collected: %+v", refs)
	}

	deep := []wfStep{{Type: "wait", Seconds: 1}}
	for range wfMaxDepth {
		deep = []wfStep{{Type: "if", If: &wfCond{Type: "time_between", From: "00:00", To: "01:00"}, Then: deep}}
	}
	many := make([]wfStep, wfMaxSteps+1)
	for i := range many {
		many[i] = wfStep{Type: "wait", Seconds: 1}
	}
	for name, c := range map[string]struct {
		d    wfDef
		want string
	}{
		"unknown trigger": {wfDef{Trigger: wfTrigger{Type: "nope"}}, "unknown trigger"},
		"no steps":        {wfDef{Trigger: wfTrigger{Type: "manual"}}, "at least one step"},
		"unknown step":    {wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "rm"}}}, "unknown step"},
		"missing service": {wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "restart"}}}, "choose a service"},
		"bad url":         {wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "http", URL: "file:///etc/passwd"}}}, "http://"},
		"header injection": {wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "http", URL: "https://x",
			Headers: map[string]string{"X": "a\r\nHost: evil"}}}}, "invalid header"},
		"wait too long":   {wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "wait", Seconds: 86401}}}, "24 hours"},
		"wait can't fail": {wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "wait", Seconds: 1, OnFailure: []wfStep{{Type: "wait", Seconds: 1}}}}}, "can't fail"},
		"branches on non-if": {wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "restart", ServiceID: "a",
			Then: []wfStep{{Type: "wait", Seconds: 1}}}}}, "only If"},
		"if without condition": {wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "if"}}}, "needs a condition"},
		"too deep":             {wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: deep}, "levels deep"},
		"too many":             {wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: many}, "at most"},
		"bad schedule":         {wfDef{Trigger: wfTrigger{Type: "schedule", Every: "daily", At: "25:00"}, Steps: many[:1]}, "HH:MM"},
		"push needs service":   {wfDef{Trigger: wfTrigger{Type: "github_push"}, Steps: many[:1]}, "service"},
		"alert to nobody":      {wfDef{Trigger: wfTrigger{Type: "manual"}, Steps: []wfStep{{Type: "alert"}}}, "who gets"},
	} {
		err := c.d.check(newRefs())
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want error containing %q", name, err, c.want)
		}
	}
}

func TestExpand(t *testing.T) {
	vars := map[string]string{"service": "api", "run_url": "https://orbit/run/1", "monitor": ""}
	data := map[string]any{
		"status": "failed", "n": 3.0, "ok": true,
		"commit": map[string]any{"author": map[string]any{"name": "ada"}},
		"evil":   "{{run_url}} {{data.status}}",
		"list":   []any{"a", "b"},
	}
	for in, want := range map[string]string{
		"{{service}} {{ data.status }}":         "api failed",
		"{{data.commit.author.name}}":           "ada",
		"{{data.commit.author}}":                `{"name":"ada"}`,
		"{{data.n}}/{{data.ok}}/{{data.list}}":  `3/true/["a","b"]`,
		"[{{data.missing}}][{{data.status.x}}]": "[][]",
		"[{{data.commit.nope.deeper}}]":         "[]",
		"{{monitor}}":                           "",
		"{{unknown}} {{ service":                "{{unknown}} {{ service",
		"{{data.evil}}":                         "{{run_url}} {{data.status}}", // substituted text is not expanded again
		"{{data.status | upper}} {{1+1}}":       "{{data.status | upper}} {{1+1}}",
	} {
		if got := expand(in, vars, data); got != want {
			t.Errorf("expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScheduleSlot(t *testing.T) {
	at := func(s string) time.Time { v, _ := time.Parse("2006-01-02 15:04", s); return v }
	now := at("2026-11-04 10:20") // a Wednesday
	for _, c := range []struct {
		tr   wfTrigger
		want string
	}{
		{wfTrigger{Every: "hourly", At: "00:15"}, "2026-11-04 10:15"},
		{wfTrigger{Every: "hourly", At: "00:45"}, "2026-11-04 09:45"},
		{wfTrigger{Every: "daily", At: "09:00"}, "2026-11-04 09:00"},
		{wfTrigger{Every: "daily", At: "23:30"}, "2026-11-03 23:30"},
		{wfTrigger{Every: "weekly", At: "08:00", Weekday: 1}, "2026-11-02 08:00"},
		{wfTrigger{Every: "weekly", At: "11:00", Weekday: 3}, "2026-10-28 11:00"},
		{wfTrigger{Every: "weekly", At: "10:20", Weekday: 3}, "2026-11-04 10:20"},
	} {
		if got := scheduleSlot(c.tr, now); !got.Equal(at(c.want)) {
			t.Errorf("%+v: got %s, want %s", c.tr, got, c.want)
		}
	}
	daily := wfTrigger{Every: "daily", At: "09:00"}
	if scheduleSlot(daily, now).After(at("2026-11-04 09:00")) {
		t.Error("a slot that already ran must not be due again")
	}
	if !scheduleSlot(daily, now).After(at("2026-11-03 09:00")) {
		t.Error("today's slot should be due after yesterday's ran")
	}
}

func TestConditions(t *testing.T) {
	at := func(s string) time.Time { v, _ := time.Parse("15:04", s); return v }
	for _, c := range []struct {
		from, to, now string
		want          bool
	}{
		{"09:00", "17:00", "09:00", true}, {"09:00", "17:00", "17:00", false}, {"09:00", "17:00", "08:59", false},
		{"22:00", "06:00", "23:30", true}, {"22:00", "06:00", "05:59", true}, {"22:00", "06:00", "12:00", false},
	} {
		if got := timeBetween(c.from, c.to, at(c.now)); got != c.want {
			t.Errorf("timeBetween(%s, %s, %s) = %v", c.from, c.to, c.now, got)
		}
	}
	vars := map[string]string{"service": "api"}
	data := map[string]any{"status": "failed", "error": "health check timed out"}
	for _, c := range []struct {
		cond wfCond
		want bool
	}{
		{wfCond{Type: "field_equals", Field: "data.status", Value: "failed"}, true},
		{wfCond{Type: "field_equals", Field: "data.status", Value: "fail"}, false},
		{wfCond{Type: "field_contains", Field: "data.error", Value: "timed out"}, true},
		{wfCond{Type: "field_equals", Field: "service", Value: "api"}, true},
		{wfCond{Type: "field_equals", Field: "data.missing", Value: ""}, true},
	} {
		if got := fieldCond(c.cond, vars, data); got != c.want {
			t.Errorf("%+v = %v", c.cond, got)
		}
	}
	for _, bad := range []*wfCond{nil, {Type: "eval"}, {Type: "disk_below", ServerID: "s", Percent: 120}, {Type: "time_between", From: "9:00", To: "10:00"}} {
		if checkCond(bad) == nil {
			t.Errorf("checkCond(%+v) accepted", bad)
		}
	}
	tr := wfTrigger{Type: "monitor_down", MonitorID: "m1"}
	if !tr.matches(map[string]any{"monitor_id": "m1"}) || tr.matches(map[string]any{"monitor_id": "m2"}) || !(wfTrigger{Type: "monitor_down"}).matches(map[string]any{"monitor_id": "m2"}) {
		t.Error("trigger scope matching")
	}
}
