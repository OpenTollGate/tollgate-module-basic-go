package config_manager

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
)

// IdentitiesConfig holds all user and system identities.
type IdentitiesConfig struct {
	ConfigVersion    string           `json:"config_version"`
	OwnedIdentities  []OwnedIdentity  `json:"owned_identities"`
	PublicIdentities []PublicIdentity `json:"public_identities"`
}

// OwnedIdentity represents an identity with a private key.
type OwnedIdentity struct {
	Name       string `json:"name"`
	PrivateKey string `json:"privatekey"`
}

// PublicIdentity represents a public-facing identity.
type PublicIdentity struct {
	Name             string `json:"name"`
	PubKey           string `json:"pubkey,omitempty"`
	LightningAddress string `json:"lightning_address,omitempty"`
}

// NewDefaultIdentitiesConfig creates an IdentitiesConfig with default values.
func NewDefaultIdentitiesConfig() *IdentitiesConfig {
	return &IdentitiesConfig{
		ConfigVersion: "v0.0.1",
		OwnedIdentities: []OwnedIdentity{
			{
				Name:       "merchant",
				PrivateKey: "e71fa3f07bea377a40ae2270aad2ab26c57b9929c46d16e76635e47cdbcba5da", // Placeholder, should be generated
			},
		},
		PublicIdentities: []PublicIdentity{
			{
				Name:             "developer",
				LightningAddress: "tollgate@minibits.cash",
			},
			{
				Name:   "trusted_maintainer_1",
				PubKey: "5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a",
			},
			{
				Name:             "owner",
				PubKey:           "[on_setup]",
				LightningAddress: "tollgate@minibits.cash",
			},
		},
	}
}

// LoadIdentities loads and parses identities.json.
func LoadIdentities(filePath string) (*IdentitiesConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var identitiesConfig IdentitiesConfig
	err = json.Unmarshal(data, &identitiesConfig)
	if err != nil {
		return nil, err
	}
	return &identitiesConfig, nil
}

// SaveIdentities saves identities.json.
func SaveIdentities(filePath string, identitiesConfig *IdentitiesConfig) error {
	data, err := json.MarshalIndent(identitiesConfig, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0644)
}

// EnsureDefaultIdentities ensures a default identities.json exists, loading from file if present.
func EnsureDefaultIdentities(filePath string) (*IdentitiesConfig, error) {
	defaultIdentitiesConfig := NewDefaultIdentitiesConfig()

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultIdentitiesConfig, SaveIdentities(filePath, defaultIdentitiesConfig)
		}
		return nil, err
	}

	var identitiesConfig IdentitiesConfig
	if err := json.Unmarshal(data, &identitiesConfig); err != nil || identitiesConfig.ConfigVersion != defaultIdentitiesConfig.ConfigVersion {
		if backupErr := backupAndLog(filePath, "/etc/tollgate/config_backups", "identities", defaultIdentitiesConfig.ConfigVersion); backupErr != nil {
			log.Printf("CRITICAL: Failed to backup and remove invalid identities config: %v", backupErr)
			return nil, backupErr
		}
		return defaultIdentitiesConfig, SaveIdentities(filePath, defaultIdentitiesConfig)
	}
	return &identitiesConfig, nil
}

// GetPublicIdentity retrieves a PublicIdentity by name.
func (ic *IdentitiesConfig) GetPublicIdentity(name string) (*PublicIdentity, error) {
	for _, id := range ic.PublicIdentities {
		if id.Name == name {
			return &id, nil
		}
	}
	return nil, fmt.Errorf("public identity not found: %s", name)
}

// GetOwnedIdentity retrieves an OwnedIdentity by name.
func (ic *IdentitiesConfig) GetOwnedIdentity(name string) (*OwnedIdentity, error) {
	for _, id := range ic.OwnedIdentities {
		if id.Name == name {
			return &id, nil
		}
	}
	return nil, fmt.Errorf("owned identity not found: %s", name)
}
