# config_manager HLDD

## Overview

The `config_manager` package provides a `ConfigManager` struct that manages configuration stored in multiple files, including a main configuration file and an installation configuration file (`install.json`). It references package information through NIP94 event IDs and now includes handling for the release channel.

## Responsibilities

- Initialize with a specific file path for the main configuration.
- Load configuration from the main configuration file and installation configuration from `install.json`.
- Save configuration to the respective files.
- Ensure a default configuration exists for both main and installation configurations.
- Store and manage `release_channel` information for packages.

## Interfaces

- `NewConfigManager(filePath string) (*ConfigManager, error)`: Creates a new `ConfigManager` instance with the specified file path.
- `LoadConfig() (*Config, error)`: Reads the main configuration from the managed file.
- `SaveConfig(config *Config) error`: Writes the main configuration to the managed file.
- `LoadInstallConfig() (*InstallConfig, error)`: Reads the installation configuration from `install.json`.
- `SaveInstallConfig(installConfig *InstallConfig) error`: Writes the installation configuration to `install.json`.
- `EnsureDefaultConfig() (*Config, error)`: Ensures a default main configuration exists, creating it if necessary.

## Handling Release Channel

The `config_manager` will be updated to store the `release_channel` information in the installation configuration (`install.json`).

## Centralized Rate Limiting for relayPool

To address the 'too many concurrent REQs' error, we will implement centralized rate limiting for `relayPool` within `config_manager`. This involves initializing `relayPool` in `config_manager` and providing a controlled access mechanism through a member function. This approach ensures that all services using `relayPool` are rate-limited, preventing excessive concurrent requests to relays.
