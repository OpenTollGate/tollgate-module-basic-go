import pytest
import subprocess
import time
import logging

# --- Configuration ---
DOWNLOAD_URL = "https://nbg1-speed.hetzner.com/100MB.bin"
TEST_TIMEOUT = 300  # 5 minutes
PING_HOST = "8.8.8.8" # Host to ping for connectivity check

# --- Optional: Enable verbose logging ---
# logging.basicConfig(level=logging.DEBUG)

def test_data_allotment():
    """
    Tests the data allotment enforcement by the TollGate using a curl download.
    """
    print("\n--- Starting Data Measurement Test ---")
    print("1. Please connect your device to the TollGate Wi-Fi network.")
    print("2. Open a browser and pay the captive portal for a data-based session.")
    
    input("--> Press Enter once you have successfully paid and have internet access...")

    # --- Pre-Test Connectivity Check ---
    print(f"\n--> [PRE-CHECK] Verifying initial internet connectivity by pinging {PING_HOST}...")
    ping_command = ["ping", "-c", "1", "-W", "5", PING_HOST]
    try:
        subprocess.run(ping_command, check=True, capture_output=True, text=True)
        print(f"--> [PRE-CHECK] Initial ping to {PING_HOST} SUCCEEDED. Proceeding with test.")
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        print(f"--> [PRE-CHECK] Initial ping to {PING_HOST} FAILED.")
        pytest.fail("Cannot establish initial internet connection. Aborting test.")

    print("\n--> [TEST] Starting large file download to generate traffic...")
    print(f"--> [TEST] This will run for a maximum of {TEST_TIMEOUT} seconds.")
    print("--> [TEST] The test will pass if the download is interrupted and internet access is cut.")

    process = None
    try:
        # Use curl to download a large file. This will be interrupted when the gate closes.
        # The -# flag enables a progress bar.
        command = f"curl -# -o /dev/null {DOWNLOAD_URL}"
        print(f"--> [TEST] Executing local command: {command}")
        
        # We let stderr pass through so the curl progress bar is visible.
        process = subprocess.Popen(command, shell=True, stdout=subprocess.PIPE, stderr=None)
        process.wait(timeout=TEST_TIMEOUT)

        # --- Verification Step ---
        print("\n--> [VERIFY] Download process finished or was terminated. Checking internet connectivity...")
        ping_command = ["ping", "-c", "1", "-W", "5", PING_HOST]
        try:
            subprocess.run(ping_command, check=True, capture_output=True, text=True)
            # If ping succeeds, the connection is still active, which is a failure.
            print(f"--> [VERIFY] Ping to {PING_HOST} SUCCEEDED.")
            print("❌ FAILURE: Internet connection is still active after download stopped.")
            print("--> This likely means the download completed before the allotment was used.")
            pytest.fail("Internet connection was not terminated by the TollGate.")
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
            # If ping fails, the connection is down, which is the desired outcome.
            print(f"--> [VERIFY] Ping to {PING_HOST} FAILED as expected.")
            print("✅ SUCCESS: Internet connection was correctly terminated by the TollGate.")
            assert True

    except subprocess.TimeoutExpired:
        print(f"\n❌ FAILURE: Test timed out after {TEST_TIMEOUT} seconds.")
        print("--> The download was not terminated, which may indicate an issue with data allotment enforcement.")
        if process:
            process.kill()
        pytest.fail(f"Download was not terminated within the {TEST_TIMEOUT}s timeout.")
    except Exception as e:
        print(f"\n❌ An unexpected error occurred: {e}")
        if process:
            process.kill()
        pytest.fail(f"Test failed with an unexpected error: {e}")