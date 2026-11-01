// Email templates: designed in React (web/emails), rendered by `npm run emails` into Go templates here.
package server

import (
	"bytes"
	"embed"
	htmltemplate "html/template"
	texttemplate "text/template"
)

//go:embed emails/*
var emailFS embed.FS

var (
	emailLogo, _ = emailFS.ReadFile("emails/logo.png")
	emailHTML    = htmltemplate.Must(htmltemplate.New("").Option("missingkey=error").ParseFS(emailFS, "emails/*.html"))
	emailText    = texttemplate.Must(texttemplate.New("").Option("missingkey=error").ParseFS(emailFS, "emails/*.txt"))
)

// renderEmail fills emails/<name>.{html,txt}. html/template escapes every value for its context.
func (s *Server) renderEmail(name string, data map[string]string) (text, html string, err error) {
	d := map[string]string{"AppURL": s.cfg.AppURL}
	for k, v := range data {
		d[k] = v
	}
	var h, t bytes.Buffer
	if err := emailHTML.ExecuteTemplate(&h, name+".html", d); err != nil {
		return "", "", err
	}
	if err := emailText.ExecuteTemplate(&t, name+".txt", d); err != nil {
		return "", "", err
	}
	return t.String(), h.String(), nil
}

func (s *Server) sendTemplate(to []string, subject, name string, data map[string]string) error {
	text, html, err := s.renderEmail(name, data)
	if err != nil {
		return err
	}
	return s.sendEmail(to, subject, text, html)
}
