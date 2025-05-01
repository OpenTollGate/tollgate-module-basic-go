## Config Struct

The `Config` struct will hold the configuration parameters as defined in the existing configuration file:

```json
{
  "accepted_mint": "https://mint.minibits.cash/Bitcoin",
  "bragging": {
    "enabled": true,
    "fields": [
      "amount",
      "mint",
      "duration"
    ]
  },
  "min_payment": 1,
  "mint_fee": 0,
  "package_info": {
    "timestamp": 1745741060,
    "version": "0.0.1+1cac608",
    "branch": "main",
    "arch": "aarch64_cortex-a53"
  },
  "price_per_minute": 1,
  "relays": [
    "wss://relay.damus.io",
    "wss://nos.lol",
    "wss://nostr.mom"
  ],
  "tollgate_private_key": "8a45d0add1c7ddf668f9818df550edfa907ae8ea59d6581a4ca07473d468d663",
  "trusted_maintainers": [
    "5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a"
  ]
}
```

## ConfigManager Struct

The `ConfigManager` struct will manage the configuration file.

## NewConfigManager Function

- Creates a new `ConfigManager` instance with the specified file path.
- Calls `EnsureDefaultConfig` to ensure a valid configuration exists.

## LoadConfig Function

- Reads the configuration from the managed file.
- Returns the `Config` struct or an error if the file is invalid.

## SaveConfig Function

- Marshals the `Config` struct into JSON data.
- Writes the JSON data to the managed file.
- Returns an error if the write operation fails.

## EnsureDefaultConfig Function

- Attempts to load the configuration from the managed file.
- If no configuration file exists or is invalid, creates a default `Config` struct based on the existing configuration structure.
- Saves the default configuration to the managed file.
- Returns the loaded or default configuration and any error encountered.