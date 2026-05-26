package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigPathIntegration_ConfigManagerReadsFromExactFile(t *testing.T) {
	testDir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", testDir)

	configPath := filepath.Join(testDir, "config.json")
	installPath := filepath.Join(testDir, "install.json")
	identitiesPath := filepath.Join(testDir, "identities.json")

	testConfig := config_manager.Config{
		ConfigVersion: "v0.0.7",
		LogLevel:      "info",
		AcceptedMints: []config_manager.MintConfig{
			{
				URL:                     "https://test-mint-integration.example.com",
				MinBalance:              10,
				BalanceTolerancePercent: 5,
				PayoutIntervalSeconds:   30,
				MinPayoutAmount:         50,
				PricePerStep:            1,
				PriceUnit:               "sats",
			},
		},
		ProfitShare: []config_manager.ProfitShareConfig{
			{Factor: 1.0, Identity: "owner"},
		},
		StepSize:  4096,
		Margin:    0.1,
		Metric:    "bytes",
		ShowSetup: true,
	}

	data, err := json.Marshal(testConfig)
	require.NoError(t, err)
	err = os.WriteFile(configPath, data, 0644)
	require.NoError(t, err)

	cm, err := config_manager.NewConfigManager(configPath, installPath, identitiesPath)
	require.NoError(t, err)

	loaded := cm.GetConfig()
	require.NotNil(t, loaded)

	assert.Equal(t, configPath, cm.ConfigFilePath, "ConfigManager should report the exact file path we gave it")
	assert.Len(t, loaded.AcceptedMints, 1, "should load the single test mint from the written config")
	assert.Equal(t, "https://test-mint-integration.example.com", loaded.AcceptedMints[0].URL)
	assert.Equal(t, uint64(10), loaded.AcceptedMints[0].MinBalance)
}

func TestConfigPathIntegration_ProductionPathWhenEnvUnset(t *testing.T) {
	os.Unsetenv("TOLLGATE_TEST_CONFIG_DIR")

	configPath, _, _ := getTollgatePaths()
	assert.Equal(t, "/etc/tollgate/config.json", configPath)
}

func TestConfigPathIntegration_TestDirPathWhenEnvSet(t *testing.T) {
	testDir := t.TempDir()
	orig := os.Getenv("TOLLGATE_TEST_CONFIG_DIR")
	defer os.Setenv("TOLLGATE_TEST_CONFIG_DIR", orig)

	os.Setenv("TOLLGATE_TEST_CONFIG_DIR", testDir)
	configPath, _, _ := getTollgatePaths()
	assert.Equal(t, filepath.Join(testDir, "config.json"), configPath)
}

func TestConfigPathIntegration_DefaultConfigMatchesWrittenFile(t *testing.T) {
	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "config.json")
	installPath := filepath.Join(testDir, "install.json")
	identitiesPath := filepath.Join(testDir, "identities.json")

	cm, err := config_manager.NewConfigManager(configPath, installPath, identitiesPath)
	require.NoError(t, err)

	config := cm.GetConfig()
	require.NotNil(t, config)

	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var fromFile config_manager.Config
	err = json.Unmarshal(raw, &fromFile)
	require.NoError(t, err)

	assert.Equal(t, len(config.AcceptedMints), len(fromFile.AcceptedMints),
		"in-memory config mint count should match file mint count")
	for i, m := range config.AcceptedMints {
		assert.Equal(t, m.URL, fromFile.AcceptedMints[i].URL,
			"mint %d URL should match between in-memory and file", i)
	}
}

func TestConfigPathIntegration_NoTestMintOnMainBranch(t *testing.T) {
	orig := config_manager.GitBranch
	defer func() { config_manager.GitBranch = orig }()

	config_manager.GitBranch = "main"
	cfg := config_manager.NewDefaultConfig()
	for _, m := range cfg.AcceptedMints {
		assert.NotEqual(t, "https://nofee.testnut.cashu.space", m.URL,
			"test mint must not appear in default config on main branch")
	}
}

func TestConfigPathIntegration_TestMintOnFeatureBranch(t *testing.T) {
	orig := config_manager.GitBranch
	defer func() { config_manager.GitBranch = orig }()

	config_manager.GitBranch = "feature/test-branch"
	cfg := config_manager.NewDefaultConfig()
	found := false
	for _, m := range cfg.AcceptedMints {
		if m.URL == "https://nofee.testnut.cashu.space" {
			found = true
			break
		}
	}
	assert.True(t, found, "test mint must appear in default config on non-main branch")
}
