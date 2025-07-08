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
  - File exists; it should not be overwritten.

## 2. `config_manager_install.go`

### 2.1. `TestLoadInstallConfig`
- **Objective:** Verify loading of `install.json`.
- **Cases:**
  - File not found.
  - File contains invalid JSON.
  - File is valid.

### 2.2. `TestSaveInstallConfig`
- **Objective:** Verify saving of `install.json`.
- **Cases:**
  - Save a valid config and verify the file content.

### 2.3. `TestEnsureDefaultInstall`
- **Objective:** Verify the creation of a default `install.json`.
- **Cases:**
  - No file exists; a default should be created.
  - File exists; it should not be overwritten.

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
  - File exists; it should not be overwritten.

## 4. `config_manager.go`

### 4.1. `TestNewConfigManager`
- **Objective:** Test the main constructor.
- **Cases:**
  - Test with a legacy `config.json` to trigger migration.
  - Test with the new three-file configuration.
  - Test with no configuration files present.

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

## 5. Migration

### 5.1. `TestMigrateV3ToV4`
- **Objective:** Verify the migration logic.
- **Cases:**
  - A full v3 config is correctly migrated.
  - A minimal v3 config is correctly migrated.
  - Profit share and trusted maintainers are correctly moved to public identities.