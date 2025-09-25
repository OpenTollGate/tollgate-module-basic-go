import pytest
import subprocess
import tempfile
import shutil
import os
import time

LOCAL_MINT_URL = "https://nofees.testnut.cashu.space"
TOLLGATE_NETWORK_PREFIX = "TollGate-"
INTERFACE = "wlp59s0"  # This should be configurable
BLOSSOM_URL = "https://blossom.swissdash.site/21d180236f012e5ece0e7881b3602779f14d6b1d8deaaf63914aa0f5b67d4bc3.ipk"
ROUTER_PASSWORD = "c03rad0r123"

@pytest.fixture(scope="session")
def ecash_wallet():
    """Create a temporary e-cash wallet funded from the public test mint."""
    wallet_dir = tempfile.mkdtemp()
    print(f"Created temporary e-cash wallet directory: {wallet_dir}")

    try:
        # Mint some tokens to the wallet
        mint_result = subprocess.run(
            ["cdk-cli", "-w", wallet_dir, "mint", LOCAL_MINT_URL, "1000"],
            capture_output=True,
            text=True,
            check=True
        )
        print(f"Minted tokens to wallet: {mint_result.stdout}")

        yield wallet_dir

    finally:
        # Clean up the temporary directory
        if os.path.exists(wallet_dir):
            shutil.rmtree(wallet_dir)
            print(f"Cleaned up e-cash wallet directory: {wallet_dir}")

@pytest.fixture(scope="session")
def test_token(ecash_wallet):
    """Generate a test token."""
    try:
        # Send tokens from the wallet
        send_process = subprocess.Popen(
            ["cdk-cli", "-w", ecash_wallet, "send", "--mint-url", LOCAL_MINT_URL],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True
        )

        # Send the amount to stdin
        send_output, send_error = send_process.communicate(input="100\n")

        if send_process.returncode != 0:
            raise Exception(f"Send command failed: {send_error}")

        # Extract the token from the send output
        token_lines = send_output.strip().split('\n')
        token = None
        for line in reversed(token_lines):
            if line.startswith("cashu"):
                token = line
                break
        
        if token is None:
            raise Exception("Token not found in send output")

        return token
            
    except Exception as e:
        raise Exception(f"Failed to generate test token: {e}")

def get_current_wifi_connection():
    """Get the current active WiFi connection."""
    try:
        result = subprocess.run(
            ["nmcli", "connection", "show", "--active"],
            capture_output=True,
            text=True,
            check=True
        )
        for line in result.stdout.split('\n'):
            if 'wifi' in line:
                return line.split()[0]
    except subprocess.CalledProcessError:
        pass
    return ""

def find_tollgate_networks():
    """Find all available WiFi networks with SSID starting with 'TollGate-'."""
    try:
        # Get list of available WiFi networks
        result = subprocess.run(
            ["nmcli", "device", "wifi", "list"],
            capture_output=True,
            text=True,
            check=True
        )
        
        tollgate_networks = []
        for line in result.stdout.split('\n'):
            if TOLLGATE_NETWORK_PREFIX in line:
                # Extract the SSID by splitting on whitespace and finding the column with our prefix
                parts = line.split()
                for i, part in enumerate(parts):
                    if part.startswith(TOLLGATE_NETWORK_PREFIX):
                        tollgate_networks.append(part)
                        break
        
        # Sort the networks
        tollgate_networks.sort()
        return tollgate_networks
            
    except subprocess.CalledProcessError as e:
        raise Exception(f"Failed to scan for networks: {e}")

@pytest.fixture(scope="session")
def tollgate_networks():
    """Find and return a sorted list of available TollGate networks."""
    networks = find_tollgate_networks()
    print(f"Found TollGate networks: {networks}")
    return networks

def connect_to_network(ssid):
    """Connect to a WiFi network with the given SSID."""
    max_attempts = 10
    attempt = 0
    
    while attempt < max_attempts:
        try:
            # Disconnect from current network
            subprocess.run(
                ["nmcli", "device", "disconnect", INTERFACE],
                stderr=subprocess.DEVNULL
            )
            
            # Connect to the specified network
            result = subprocess.run(
                ["nmcli", "device", "wifi", "connect", ssid],
                capture_output=True,
                text=True
            )
            
            if result.returncode == 0:
                print(f"Successfully connected to network: {ssid}")
                return ssid
            else:
                print(f"Failed to connect to network: {ssid}, attempt {attempt + 1}/{max_attempts}")
                
        except subprocess.CalledProcessError as e:
            print(f"Error connecting to network {ssid}: {e}")
            
        # If we didn't connect, wait a bit and try again
        attempt += 1
        if attempt < max_attempts:
            time.sleep(2)  # Wait 2 seconds before retrying
    
    raise Exception(f"Failed to connect to network {ssid} after {max_attempts} attempts")

@pytest.fixture(scope="function")
def connect_to_tollgate_network(tollgate_networks):
    """Connect to the first available TollGate network and restore the previous connection."""
    if not tollgate_networks:
        raise Exception("No TollGate networks found")
    
    # Get the current network connection
    previous_connection = get_current_wifi_connection()
    print(f"Current network connection: {previous_connection}")
    
    # Connect to the first TollGate network
    network = tollgate_networks[0]
    connected_network = connect_to_network(network)
    
    yield connected_network
    
    # Reconnect to the previous network
    if previous_connection:
        try:
            subprocess.run(
                ["nmcli", "connection", "up", previous_connection],
                check=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL
            )
            print(f"Successfully reconnected to previous network: {previous_connection}")
        except subprocess.CalledProcessError:
            print(f"Failed to reconnect to previous network: {previous_connection}")

def install_package(ip_address):
    """Install the latest package on the router."""
    try:
        # Instead of just pinging, let's try to establish an SSH connection directly
        # This is a more reliable way to check if the router is reachable and ready
        print(f"Attempting to connect to router at {ip_address}...")
        
        # SSH into the router and install the package
        print(f"Connecting to router at {ip_address}...")
        ssh_command = [
            "sshpass", "-p", ROUTER_PASSWORD,
            "ssh", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=10",
            f"root@{ip_address}",
            f"cd /tmp/ && wget {BLOSSOM_URL} && opkg install *.ipk"
        ]
        result = subprocess.run(ssh_command, capture_output=True, text=True, check=True)
        print(f"Package installed successfully on {ip_address}")
        return result.stdout
    except subprocess.CalledProcessError as e:
        raise Exception(f"Failed to install package on {ip_address}: {e}\nStdout: {e.stdout}\nStderr: {e.stderr}")

def get_router_ip(interface=INTERFACE):
    """Get the router's IP address for the current network."""
    try:
        # Get the default gateway (router IP) using ip route
        result = subprocess.run(
            ["ip", "route", "get", "1.1.1.1"],
            capture_output=True,
            text=True,
            check=True
        )
        output_lines = result.stdout.split('\n')
        # Parse the output to find the gateway
        # Example output: "1.1.1.1 via 192.168.13.1 dev wlp59s0 src 192.168.13.199 uid 1000"
        for line in output_lines:
            if "via" in line:
                parts = line.split()
                # Find the "via" keyword and get the next part (gateway IP)
                for i, part in enumerate(parts):
                    if part == "via":
                        router_ip = parts[i + 1]
                        print(f"Determined router IP: {router_ip} (using ip route)")
                        return router_ip
        raise Exception("Could not find gateway in ip route output")
    except (subprocess.CalledProcessError, ValueError, IndexError) as e:
        raise Exception(f"Failed to determine router IP: {e}")

@pytest.fixture(scope="function")
def install_packages_on_tollgates(tollgate_networks):
    """Find all TollGate networks, connect to each one, and install the latest package."""
    if not tollgate_networks:
        raise Exception("No TollGate networks found")
    
    # Get the current network connection
    previous_connection = get_current_wifi_connection()
    print(f"Current network connection: {previous_connection}")
    
    installed_routers = []
    
    for network in tollgate_networks:
        try:
            # Connect to the TollGate network
            connected_network = connect_to_network(network)
            
            # Get the router's IP address dynamically
            router_ip = get_router_ip()
            
            # Install the package on the router
            install_package(router_ip)
            installed_routers.append(router_ip)
            
        except Exception as e:
            print(f"Failed to install package on network {network}: {e}")
            
    yield installed_routers
    
    # Reconnect to the previous network
    if previous_connection:
        try:
            subprocess.run(
                ["nmcli", "connection", "up", previous_connection],
                check=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL
            )
            print(f"Successfully reconnected to previous network: {previous_connection}")
        except subprocess.CalledProcessError:
            print(f"Failed to reconnect to previous network: {previous_connection}")
