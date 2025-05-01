# config_manager LLDD

## Config Struct

The `Config` struct will hold the configuration parameters.

## ConfigManager Struct

The `ConfigManager` struct will manage the configuration file.

## LoadConfig Function

- Reads the configuration from the managed file.
- Returns the `Config` struct or an error if the file is invalid.

## SaveConfig Function

- Marshals the `Config` struct into JSON data.
- Writes the JSON data to the managed file.
- Returns an error if the write operation fails.

## EnsureDefaultConfig Function

- Attempts to load the configuration from the managed file.
- If no configuration file exists, creates a default `Config` struct.
- Saves the default configuration to the managed file.
- Returns the loaded or default configuration and any error encountered.