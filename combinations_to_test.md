# Test Combinations for Janitor Module
## Initial Scenarios

| Channel | Push to Branch    | Previous Installation Method | NIP94 Event ID Updated as Expected | New Update Installed as Expected | Test Passed |
| ------- | ----------------- | ---------------------------- | ---------------------------------- | -------------------------------- | ----------- |
| dev     | Already Installed | Manual                       |                                    |                                  |             |
| dev     | Already Installed | Automatic                    |                                    |                                  |             |

## Additional Scenarios from git diff Analysis

| Channel | Push to Branch    | Version Format           | NIP94EventID State | Expected Outcome                                                                   | Test Passed |
| ------- | ----------------- | ------------------------ | ------------------ | ---------------------------------------------------------------------------------- | ----------- |
| dev     | New Branch        | branch-commit_count-hash | unknown            | Don't update, because the currently installed branch is different                  |             |
| stable  | New Branch        | version_number           | known              | Update Successfully                                                                |             |
| dev     | Already Installed | branch-commit_count-hash | known              | Update Successfully if new commits                                                 |             |
| dev     | Already Installed | branch-commit_count-hash | unknown            | Update Successfully if new commits                                                 |             |
| stable  | New Branch        | version_number           | unknown            | Update if version number is higher, irrespective of branch name, and NIP94 eventID |             |
