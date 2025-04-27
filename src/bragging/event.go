package bragging

import (
	"fmt"
	"github.com/nbd-wtf/go-nostr"
	"strings"
)

func (s *Service) CreateEvent(saleData map[string]interface{}) (*nostr.Event, error) {
	if !s.config.Enabled {
		return nil, nil
	}

	event := &nostr.Event{
		Kind:      1,
		CreatedAt: nostr.Now(),
		Tags:      make(nostr.Tags, 0),
		Content:   "",
	}

	var content string
	for _, field := range s.config.Fields {
		if value, exists := saleData[field]; exists {
			event.Tags = append(event.Tags, nostr.Tag{field, fmt.Sprint(value)})
			content += fmt.Sprintf("%s: %v, ", field, value)
		}
	}

	// Trim the trailing comma and space if content is not empty
	if content != "" {
		content = strings.TrimSuffix(content, ", ")
	}

	event.Content = content

	event.Sign(s.privateKey)
	return event, nil
}
