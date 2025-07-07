# Manual Testing Plan: Automatic Public Key Synchronization

## 1. Objective

To manually verify that the automatic public key synchronization mechanism correctly, robustly, and efficiently updates the `identities.json` file when `config.json` is modified.

## 2. Prerequisites

*   A running instance of the application on an OpenWRT device or a simulated environment.
*   Access to the file system to directly edit `config.json` and `identities.json`.
*   A tool to monitor the application's log output (e.g., `logread -f`).

## 3. Test Cases

### Test Case 1: Successful Synchronization on Valid Key Change

*   **Objective**: Verify that a valid change to the private key in `config.json` triggers a correct update to the public key in `identities.json`.
*   **Steps**:
    1.  Start the application and monitor the logs.
    2.  Note the initial `TollgatePrivateKey` in `config.json` and the corresponding `npub` for the "operator" in `identities.json`.
    3.  Generate a new, valid Nostr private key.
    4.  Replace the `TollgatePrivateKey` in `config.json` with the new key and save the file.
    5.  Observe the application logs for a message indicating that a change was detected and a sync was triggered.
    6.  Open `identities.json` and verify that the `npub` for the "operator" has been updated to the public key corresponding to the new private key.
*   **Expected Result**: The `npub` in `identities.json` is correctly updated, and the logs show a successful synchronization message.

### Test Case 2: Handling of Invalid Private Key

*   **Objective**: Ensure the system handles an invalid private key gracefully without crashing or corrupting `identities.json`.
*   **Steps**:
    1.  Start the application and monitor the logs.
    2.  Edit `config.json` and replace the `TollgatePrivateKey` with an invalid string (e.g., "invalid-key").
    3.  Save the `config.json` file.
    4.  Observe the logs for an error message indicating that the private key is invalid and the sync is being aborted.
    5.  Check `identities.json` to ensure that the "operator" `npub` has *not* been changed.
*   **Expected Result**: The application logs an error but continues to run. The `identities.json` file remains unchanged.

### Test Case 3: No Change on Non-Key-Related Edits

*   **Objective**: Verify that the synchronization is not triggered unnecessarily when other fields in `config.json` are changed.
*   **Steps**:
    1.  Start the application.
    2.  Modify a field in `config.json` other than `TollgatePrivateKey` (e.g., change a relay URL).
    3.  Save the file.
    4.  While the watcher will fire, the sync logic should detect no change in the public key.
    5.  Check `identities.json` to confirm that the `npub` has not changed.
*   **Expected Result**: The `identities.json` file is not modified. The logs may show a sync was triggered, but no update was performed.

### Test Case 4: Rapid, Repeated Changes (Race Condition Test)

*   **Objective**: Test the system's resilience to rapid, repeated file saves and ensure the mutex prevents race conditions.
*   **Steps**:
    1.  Start the application and monitor the logs.
    2.  Prepare two different valid private keys.
    3.  In quick succession, save `config.json` first with one key, and then immediately with the second key. A simple shell script can be used for this:
        ```sh
        cp config.key1.json config.json && cp config.key2.json config.json
        ```
    4.  Observe the logs to see if multiple syncs are triggered and handled sequentially.
    5.  After the operations complete, check `identities.json` to ensure the `npub` corresponds to the *last* private key that was written.
*   **Expected Result**: The system remains stable, handles the syncs sequentially, and the final state of `identities.json` is consistent with the last change.

### Test Case 5: Deletion and Recreation of `config.json`

*   **Objective**: Verify the system's behavior when `config.json` is deleted and then recreated.
*   **Steps**:
    1.  Start the application.
    2.  Delete the `config.json` file.
    3.  The file watcher may stop. This is an acceptable limitation of most file watchers.
    4.  Create a new `config.json` with a valid private key.
    5.  Restart the application.
    6.  Verify that on restart, the `EnsureDefaultIdentities` (via `SyncOperatorIdentity`) correctly populates `identities.json`.
*   **Expected Result**: The watcher may fail, but the system recovers correctly on the next restart, ensuring eventual consistency.

## 4. Consolidated Manual Tests Update

Upon successful completion of these tests, the following entry should be added to `docs/manual_testing/consolidated_manual_tests.md`:

```markdown
- [ ] **Automatic Public Key Synchronization**:
    - [ ] Verify that changing the `TollgatePrivateKey` in `config.json` correctly updates the operator `npub` in `identities.json`.
    - [ ] Verify that saving an invalid private key logs an error and does not modify `identities.json`.
    - [ ] Verify that rapid, repeated changes to `config.json` are handled sequentially without race conditions.