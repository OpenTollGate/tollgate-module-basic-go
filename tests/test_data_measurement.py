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
    print("--> [TEST] The test will pass if this process is terminated before the timeout.")

    process = None
    try:
        command = f"dd if=/dev/zero | nc {SERVER_HOST} {NETCAT_PORT}"
        print(f"--> [TEST] Executing local command: {command}")
        process = subprocess.Popen(command, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

        process.wait(timeout=TEST_TIMEOUT)

        stdout, stderr = process.communicate()
        print("\n✅ SUCCESS: Data stream process terminated.")
        print("--> This indicates the TollGate likely enforced the data allotment and closed the connection.")
        if stderr:
            print(f"--> Subprocess stderr:\n{stderr.decode()}")
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