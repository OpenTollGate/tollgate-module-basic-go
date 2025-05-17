package bragging

import (
	"bytes"
	"fmt"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"log"
	"text/template"
)

var defaultTemplate = `New sale! {{.amount}} sats via {{.mint}} for {{.duration}} sec`

func renderTemplate(configManager *config_manager.ConfigManager, data map[string]interface{}) string {
	tpl := template.Must(template.New("").Parse(defaultTemplate))

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		log.Printf("Template execution error: %v. Using default values.", err)
		return fmt.Sprintf(defaultTemplate, data["amount"], data["mint"], data["duration"])
	}
	return buf.String()
}
