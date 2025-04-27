package bragging

import (
	"bytes"
	"fmt"
	"log"
	"text/template"
)

var defaultTemplate = `New sale! {{.amount}} sats via {{.mint}} for {{.duration}} sec`

func (s *Service) renderTemplate(data map[string]interface{}) string {
	tpl, err := template.New("").Parse(s.config.Template)
	if err != nil {
		log.Printf("Template parsing error: %v. Using default template.", err)
		tpl = template.Must(template.New("").Parse(defaultTemplate))
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		log.Printf("Template execution error: %v. Using default values.", err)
		return fmt.Sprintf(defaultTemplate, data["amount"], data["mint"], data["duration"])
	}
	return buf.String()
}
