package config_manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// FIPSExitConfig reserves disabled-default consumer intent. Activation is refused
// at decoding until a production applier exists; it must not silently use WAN.
type FIPSExitConfig struct {
	Enabled bool `json:"enabled"`
}

func (c *FIPSExitConfig) UnmarshalJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("fips_exit: object required")
	}
	seen := false
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return err
		}
		if key != "enabled" || seen {
			return fmt.Errorf("fips_exit: unknown or duplicate field %v", key)
		}
		seen = true
		var value interface{}
		if err := d.Decode(&value); err != nil {
			return err
		}
		enabled, ok := value.(bool)
		if !ok {
			return fmt.Errorf("fips_exit.enabled: boolean required")
		}
		if enabled {
			return fmt.Errorf("fips_exit.enabled: runtime activation is not supported yet")
		}
	}
	if _, err := d.Token(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("fips_exit: trailing data")
	}
	*c = FIPSExitConfig{}
	return nil
}
