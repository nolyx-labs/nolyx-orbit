package server

import (
	"strings"
	"testing"
	"time"
)

func TestEmailTemplates(t *testing.T) {
	s := &Server{cfg: Config{AppURL: "https://orbit.example.com"}}
	alert := s.alertEmail(alertState{Rule: "High CPU", Metric: "cpu", Threshold: 90, Value: 97.3, TargetType: "server",
		TargetName: "web-1", ProjectID: "p1"}, "firing", time.Date(2026, 10, 31, 9, 30, 0, 0, time.UTC))
	for name, data := range map[string]map[string]string{
		"password-reset":    {"Link": "https://orbit.example.com/reset-password/orb_pwr_x"},
		"invite":            {"Inviter": "ada@example.com", "Org": `Acme <script>alert(1)</script>`, "Role": "member", "Email": "bob@example.com", "Link": "https://orbit.example.com/invite/orb_inv_x"},
		alert.template:      alert.data,
		"test-notification": {"Channel": "On-call"},
		"status-confirm":    {"PageName": "Acme", "Link": "https://status.acme.io/confirm/x"},
		"status-update": {"PageName": "Acme", "Title": "API errors", "Status": "Investigating", "Color": "#dc2626",
			"Message": "We're looking into it.", "Components": "API", "Link": "https://status.acme.io/incidents/1", "Unsubscribe": "https://status.acme.io/unsubscribe/x"},
	} {
		text, html, err := s.renderEmail(name, data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(html, "{{") || strings.Contains(text, "{{") || strings.Contains(html, "<script>") {
			t.Errorf("%s: unfilled placeholder or unescaped value", name)
		}
		if l := data["Link"]; l != "" && (!strings.Contains(html, l) || !strings.Contains(text, l)) {
			t.Errorf("%s: link missing", name)
		}
	}
	if len(emailLogo) == 0 || !strings.Contains(emailHTML.DefinedTemplates(), "invite.html") {
		t.Error("logo or templates not embedded")
	}
	if _, _, err := s.renderEmail("invite", map[string]string{"Link": "x"}); err == nil {
		t.Error("missing template data rendered without error")
	}
}
