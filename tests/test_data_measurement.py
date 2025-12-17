import pytest
import paramiko
import subprocess
import time
import os
import logging

# --- Configuration ---
SERVER_HOST = "188.40.151.90"
SERVER_USER = "root"
SERVER_PORT = 22
NETCAT_PORT = 9999
TEST_TIMEOUT = 300  # 5 minutes
PING_HOST = "8.8.8.8" # Host to ping for connectivity check

# --- Optional: Enable verbose logging for Paramiko ---
# Uncomment the following lines to get detailed SSH logs written to "paramiko.log"
# logging.basicConfig(level=logging.DEBUG)
# logging.getLogger("paramiko").addHandler(logging.FileHandler("paramiko.log"))


@pytest.fixture(scope="module")
def netcat_server():
    """
    A pytest fixture to manage the netcat listener on the remote server.
    """
    ssh_client = None
    try:
        print(f"\n--> [SETUP] Connecting to server {SERVER_USER}@{SERVER_HOST}...")
        ssh_client = paramiko.SSHClient()
        ssh_client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        
        # Assumes SSH key-based authentication is set up.
        ssh_client.connect(SERVER_HOST, port=SERVER_PORT, username=SERVER_USER)
        print("--> [SETUP] SSH connection successful.")
        
        print(f"--> [SETUP] Starting netcat listener on port {NETCAT_PORT}...")
        # Ensure no old netcat process is running
        kill_command = f"pkill -f 'nc -l -p {NETCAT_PORT}'"
        ssh_client.exec_command(kill_command)
        time.sleep(1) # Give a moment for the process to be killed

        # Start netcat in the background
        command = f"nohup nc -l -p {NETCAT_PORT} > /dev/null 2>&1 &"
        ssh_client.exec_command(command)
        
        time.sleep(2)
        print("--> [SETUP] Netcat listener started on server.")

        yield ssh_client

    finally:
        if ssh_client:
            print("\n--> [TEARDOWN] Cleaning up...")
            kill_command = f"pkill -f 'nc -l -p {NETCAT_PORT}'"
            print(f"--> [TEARDOWN] Stopping netcat listener with command: {kill_command}")
            ssh_client.exec_command(kill_command)
            ssh_client.close()
            print("--> [TEARDOWN] Server connection closed.")


def test_data_allotment(netcat_server):
    """
    Tests the data allotment enforcement by the TollGate.
    """
    print("\n--- Starting Data Measurement Test ---")
    print("1. Please connect your device to the TollGate Wi-Fi network.")
    print("2. Open a browser and pay the captive portal for a data-based session.")
    
    input("--> Press Enter once you have successfully paid and have internet access...")

    print("\n--> [TEST] Starting data stream to the server...")
    print(f"--> [TEST] This will run for a maximum of {TEST_TIMEOUT} seconds.")
    print("--> [TEST] The test will pass if this process is terminated and internet access is cut.")

    process = None
    try:
        # Using pv (Pipe Viewer) to monitor the data stream.
        # You may need to install it: `sudo apt install pv`
        command = f"dd if=/dev/zero | pv | nc {SERVER_HOST} {NETCAT_PORT}"
        print(f"--> [TEST] Executing local command: {command}")
        # We use stderr=subprocess.PIPE to capture pv's output
        process = subprocess.Popen(command, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

        process.wait(timeout=TEST_TIMEOUT)

        # --- Verification Step ---
        print("\n--> [VERIFY] Data stream stopped. Checking internet connectivity...")
        ping_command = ["ping", "-c", "1", "-W", "5", PING_HOST]
        try:
            subprocess.run(ping_command, check=True, capture_output=True, text=True)
            # If ping succeeds, the connection is still active, which is a failure.
            print(f"--> [VERIFY] Ping to {PING_HOST} SUCCEEDED.")
            print("❌ FAILURE: Internet connection is still active after data stream stopped.")
            print("--> This means the stream was terminated for a reason other than the TollGate cutting access.")
            _, stderr = process.communicate()
            if stderr:
                print(f"--> Subprocess stderr that may indicate the issue:\n{stderr.decode()}")
            pytest.fail("Internet connection was not terminated by the TollGate.")
        except subprocess.CalledProcessError:
            # If ping fails, the connection is down, which is the desired outcome.
            print(f"--> [VERIFY] Ping to {PING_HOST} FAILED as expected.")
            print("✅ SUCCESS: Internet connection appears to be correctly terminated by the TollGate.")
            assert True

    except subprocess.TimeoutExpired:
        print(f"\n❌ FAILURE: Test timed out after {TEST_TIMEOUT} seconds.")
        print("--> The data stream was not terminated, which may indicate an issue with data allotment enforcement.")
        if process:
            process.kill()
        pytest.fail(f"Data stream was not terminated within the {TEST_TIMEOUT}s timeout.")
    except Exception as e:
        print(f"\n❌ An unexpected error occurred: {e}")
        if process:
            process.kill()
        pytest.fail(f"Test failed with an unexpected error: {e}")