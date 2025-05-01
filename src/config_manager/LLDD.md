# config_manager LLDD

## Config Struct

The `Config` struct will hold the configuration parameters.

## LoadConfig Function

- Reads the configuration file.
- Unmarshals the JSON data into a `Config` struct.
- Returns the `Config` struct or an error if the file is invalid.

## SaveConfig Function

- Marshals the `Config` struct into JSON data.
- Writes the JSON data to the configuration file.
- Returns an error if the write operation fails.