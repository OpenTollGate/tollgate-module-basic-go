# Unit Test Cases: Configuration Management Refactor

## 1. `config_manager_config.go`

### 1.1. `TestLoadConfig`
- **Objective:** Verify loading of `config.json`.
- **Cases:**
  - File not found.
  - File is empty.
  - File contains invalid JSON.
  - File contains a valid configuration.

### 1.2. `TestSaveConfig`
- **Objective:** Verify saving of `config.json`.
- **Cases:**
  - Save a valid config and verify the file content.

### 1.3. `TestEnsureDefaultConfig`
- **Objective:** Verify the creation of a default `config.json`.
- **Cases:**
  - No file exists; a default should be created.
  - File exists with valid JSON and correct version; it should be loaded, not overwritten.
  - File exists with malformed JSON; it should be backed up and a new default created.
  - File exists with a version mismatch; it should be backed up and a new default created.

## 2. `config_manager_install.go`

### 2.1. `TestLoadJanitorConfig`
- **Objective:** Verify loading of `janitor.json`.
- **Cases:**
  - File not found.
  - File contains invalid JSON.
  - File is valid.

### 2.2. `TestSaveJanitorConfig`
- **Objective:** Verify saving of `janitor.json`.
- **Cases:**
  - Save a valid config and verify the file content.

### 2.3. `TestEnsureDefaultJanitor`
- **Objective:** Verify the creation of a default `janitor.json`.
- **Cases:**
  - No file exists; a default should be created.
  - File exists with valid JSON and correct version; it should be loaded, not overwritten.
  - File exists with malformed JSON; it should be backed up and a new default created.
  - File exists with a version mismatch; it should be backed up and a new default created.

## 3. `config_manager_identities.go`

### 3.1. `TestLoadIdentities`
- **Objective:** Verify loading of `identities.json`.
- **Cases:**
  - File not found.
  - File contains invalid JSON.
  - File is valid.

### 3.2. `TestSaveIdentities`
- **Objective:** Verify saving of `identities.json`.
- **Cases:**
  - Save a valid config and verify the file content.

### 3.3. `TestEnsureDefaultIdentities`
- **Objective:** Verify the creation of a default `identities.json`.
- **Cases:**
  - No file exists; a default should be created.
  - File exists with valid JSON and correct version; it should be loaded, not overwritten.
  - File exists with malformed JSON; it should be backed up and a new default created.
  - File exists with a version mismatch; it should be backed up and a new default created.

## 4. `config_manager.go`

### 4.1. `TestNewConfigManager`
- **Objective:** Test the main constructor.
- **Cases:**
  - Test with all three config files being valid.
  - Test with one of the config files being invalid (e.g., malformed `config.json`).
  - Test with all configuration files being absent.

### 4.2. `TestGetIdentity`
- **Objective:** Test the retrieval of public identities.
- **Cases:**
  - Identity exists.
  - Identity does not exist.

### 4.3. `TestGetOwnedIdentity`
- **Objective:** Test the retrieval of owned identities.
- **Cases:**
  - Identity exists.
  - Identity does not exist.

## 5. Resilience Logic

### 5.1. `TestBackupAndLog`
- **Objective:** Verify the `backupAndLog` helper function.
- **Cases:**
  - **Happy Path:** Successful backup and log.
  - **Permissions Error:** Test failure when creating the backup directory.
  - **Rename Error:** Test failure when moving the file.