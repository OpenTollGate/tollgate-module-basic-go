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
IPERF_PORT = 5201 # Default iperf3 port
TEST_TIMEOUT = 300  # 5 minutes
PING_HOST = "8.8.8.8" # Host to ping for connectivity check

# --- Optional: Enable verbose logging for Paramiko ---
# Uncomment the following lines to get detailed SSH logs written to "paramiko.log"
# logging.basicConfig(level=logging.DEBUG)
# logging.getLogger("paramiko").addHandler(logging.FileHandler("paramiko.log"))


@pytest.fixture(scope="module")
def iperf_server():
    """
    A pytest fixture to manage the iperf3 server on the remote machine.
    """
    ssh_client = None
    try:
        print(f"\n--> [SETUP] Connecting to server {SERVER_USER}@{SERVER_HOST}...")
        ssh_client = paramiko.SSHClient()
        ssh_client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        
        # Assumes SSH key-based authentication is set up.
        ssh_client.connect(SERVER_HOST, port=SERVER_PORT, username=SERVER_USER)
        print("--> [SETUP] SSH connection successful.")
        
        print(f"--> [SETUP] Starting iperf3 server on port {IPERF_PORT}...")
        # Ensure no old iperf3 process is running
        kill_command = "pkill -f 'iperf3 -s'"
        ssh_client.exec_command(kill_command)
        time.sleep(1)

        # Start iperf3 as a daemon
        command = f"iperf3 -s -p {IPERF_PORT} -D"
        stdin, stdout, stderr = ssh_client.exec_command(command)
        exit_status = stdout.channel.recv_exit_status() # Wait for command to complete
        
        if exit_status == 0:
            print("--> [SETUP] iperf3 server started successfully as a daemon.")
        else:
            error_output = stderr.read().decode()
            pytest.fail(f"Failed to start iperf3 server. Exit status: {exit_status}\nError: {error_output}")

        yield ssh_client

    finally:
        if ssh_client:
            print("\n--> [TEARDOWN] Cleaning up...")
            kill_command = "pkill -f 'iperf3 -s'"
            print(f"--> [TEARDOWN] Stopping iperf3 server with command: {kill_command}")
            ssh_client.exec_command(kill_command)
            ssh_client.close()
            print("--> [TEARDOWN] Server connection closed.")


def test_data_allotment(iperf_server):
    """
    Tests the data allotment enforcement by the TollGate.
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

    print("\n--> [TEST] Starting data stream to the server...")
    print(f"--> [TEST] This will run for a maximum of {TEST_TIMEOUT} seconds.")
    print("--> [TEST] The test will pass if this process is terminated and internet access is cut.")

    process = None
    try:
        # The iperf3 client will run for the duration of the timeout, sending data.
        # The '-u' flag specifies UDP, which is a simple, fire-and-forget protocol.
        # The '-b 0' flag tells iperf to send as fast as possible.
        command = f"iperf3 -c {SERVER_HOST} -p {IPERF_PORT} -t {TEST_TIMEOUT} -b 0"
        print(f"--> [TEST] Executing local command: {command}")
        
        process = subprocess.Popen(command, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        process.wait(timeout=TEST_TIMEOUT + 10) # Add a small buffer to the timeout

        # --- Verification Step ---
        print("\n--> [VERIFY] Data stream stopped. Checking internet connectivity...")
        ping_command = ["ping", "-c", "1", "-W", "5", PING_HOST]
        try:
            subprocess.run(ping_command, check=True, capture_output=True, text=True)
            # If ping succeeds, the connection is still active, which is a failure.
            print(f"--> [VERIFY] Ping to {PING_HOST} SUCCEEDED.")
            print("❌ FAILURE: Internet connection is still active after data stream stopped.")
            print("--> This means the stream was terminated for a reason other than the TollGate cutting access.")
            stdout, stderr = process.communicate()
            if stderr:
                print(f"--> iperf3 client stderr that may indicate the issue:\n{stderr.decode()}")
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