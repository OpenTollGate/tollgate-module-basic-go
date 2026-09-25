# TollGate Automated Testing

This directory contains automated tests for the TollGate module.

## Data Measurement Test

The `test_data_measurement.py` script is designed to validate the data allotment enforcement feature of the TollGate. It orchestrates a test between a client machine (where you run the test) and a remote server to simulate a high-volume data stream.

### Prerequisites

1.  A remote server with SSH access. The test is pre-configured for a server at `188.40.151.90`.
2.  SSH key-based authentication should be configured for the `root` user on the server to allow the script to connect without a password.
3.  Python 3 and `venv` on your local machine.

### Setup and Execution

To ensure a clean and isolated environment, these tests should be run within a Python virtual environment.

**1. Create the Virtual Environment**

From the project's root directory (`tollgate-module-basic-go`), create a virtual environment named `.venv`:

```bash
python3 -m venv .venv
```

**2. Activate the Virtual Environment**

Activate the environment to use its isolated set of packages. Your terminal prompt will change to indicate that the environment is active.

```bash
source .venv/bin/activate
```

**3. Install Dependencies**

Install the necessary Python packages (`pytest` and `paramiko`) into the virtual environment:

```bash
pip install -r tests/requirements.txt
```

**4. Run the Test**

Execute the test script using `pytest`. The `-s` flag ensures you can see the interactive prompts, and the `-v` flag provides verbose output.

```bash
pytest -sv tests/test_data_measurement.py
```

The test will then guide you through the process of connecting to the TollGate Wi-Fi and paying the captive portal before it begins the data stream test.

**5. Deactivate the Environment (Optional)**

When you are finished testing, you can deactivate the virtual environment and return to your normal shell:

```bash
deactivate
```

## Router Happy-Path Harness (real hardware)

`tests/router-happy-path/` drives the customer-facing happy path against a
**live MT3000** and compares every asset the router serves against a supplied
`.apk`, byte for byte:

```bash
bash tests/router-happy-path/run.sh --apk /path/to/tollgate-wrt_<ver>_aarch64_cortex-a53.apk
bash tests/router-happy-path/selftest/run_selftest.sh   # offline, no router needed
```

Read-only by default and it spends nothing. See
`tests/router-happy-path/README.md` for the ordered check list, the traps it
encodes (ICMP is dropped; `:2050` is a stub; `/session-state` falls through on
pre-#541 builds), the opt-in paid lane, and the bench evidence (GREEN against
`v0.6.0-alpha5`, RED against an older package). Deploy, flashing and browser E2E
stay with `physical-router-test-automation`.
