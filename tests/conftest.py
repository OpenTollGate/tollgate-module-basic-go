import pytest
import subprocess
import tempfile
import shutil
import os
import time

LOCAL_MINT_URL = "https://nofees.testnut.cashu.space"
TOLLGATE_NETWORK_PREFIXES = ["TollGate-"]
INTERFACE = "wlp59s0"  # This should be configurable
BLOSSOM_URL = "https://blossom.swissdash.site/21d180236f012e5ece0e7881b3602779f14d6b1d8deaaf63914aa0f5b67d4bc3.ipk"
ROUTER_PASSWORD = "c03rad0r123"

# Global variable to store router information
router_info = {
    "ips": [],
    "ssids": []
}

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
        # Get list of available WiFi SSIDs
        result = subprocess.run(
            ["nmcli", "-f", "SSID", "device", "wifi", "list"],
            capture_output=True,
            text=True,
            check=True
        )
        
        all_ssids = result.stdout.strip().split('\n')[1:]  # Skip header
        
        tollgate_networks = []
        for ssid in all_ssids:
            ssid = ssid.strip()
            for prefix in TOLLGATE_NETWORK_PREFIXES:
                if ssid.startswith(prefix):
                    tollgate_networks.append(ssid)
                    break  # Found a match for this ssid, check next one.
        
        # Deduplicate networks by removing frequency suffixes (2.4GHz, 5GHz)
        # Group networks by their base name and only keep one per group
        deduplicated_networks = []
        network_groups = {}
        
        for ssid in tollgate_networks:
            # Remove frequency suffix to get base name
            base_name = ssid
            if ssid.endswith("-2.4GHz"):
                base_name = ssid[:-7]  # Remove "-2.4GHz"
            elif ssid.endswith("-5GHz"):
                base_name = ssid[:-5]  # Remove "-5GHz" (5 characters)
                
            # Group networks by base name
            if base_name not in network_groups:
                network_groups[base_name] = []
            network_groups[base_name].append(ssid)
        
        # For each group, select one network (prefer 5GHz over 2.4GHz if available)
        for base_name, networks in network_groups.items():
            # Prefer 5GHz networks over 2.4GHz
            preferred_network = None
            for network in networks:
                if network.endswith("-5GHz"):
                    preferred_network = network
                    break  # Prefer 5GHz, so we can stop looking
            
            # If no 5GHz network found, use the first available network
            if preferred_network is None:
                preferred_network = networks[0]
                
            deduplicated_networks.append(preferred_network)
        
        deduplicated_networks.sort()
        return deduplicated_networks
        
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


def copy_image_to_router(ip_address, image_path):
    """Copy an image file to the router's /tmp directory using ssh and cat."""
    try:
        print(f"Copying image to router at {ip_address}...")
        # Get the filename from the path
        image_filename = os.path.basename(image_path)
        remote_path = f"/tmp/{image_filename}"
        
        # Use ssh with cat to transfer the file
        ssh_command = [
            "sshpass", "-p", ROUTER_PASSWORD,
            "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
            f"root@{ip_address}",
            f"cat > {remote_path}"
        ]
        
        with open(image_path, 'rb') as f:
            result = subprocess.run(ssh_command, stdin=f, capture_output=True, check=True)
            
        print(f"Image copied successfully to {ip_address}:{remote_path}")
    except subprocess.CalledProcessError as e:
        raise Exception(f"Failed to copy image to {ip_address}:/tmp/: {e}\nStdout: {e.stdout}\nStderr: {e.stderr}")
    except IOError as e:
        raise Exception(f"Failed to read image file {image_path}: {e}")

def get_router_ip(interface=INTERFACE):
    """Get the router's IP address for the current network."""
    try:
        # Get the default gateway (router IP) using ip route
        result = subprocess.run(
            ["ip", "route", "show", "dev", interface],
            capture_output=True,
            text=True,
            check=True
        )
        
        # Parse the output to find the gateway
        # Example output: "default via 192.168.9.1 dev enx00e04c683d2d proto dhcp src 192.168.9.106 metric 100"
        for line in result.stdout.split('\n'):
            if line.startswith("default via"):
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

@pytest.fixture(scope="function")
def copy_images_to_tollgates(tollgate_networks):
    """Find all TollGate networks, connect to each one, and copy the latest image."""
    if not tollgate_networks:
        raise Exception("No TollGate networks found")
    
    # Get the current network connection
    previous_connection = get_current_wifi_connection()
    print(f"Current network connection: {previous_connection}")
    
    # Path to the image file
    image_file = "8d84c36880d3957bdb9feda280902dc023438ac8c26ba3c10eb9177953f113c1.bin"
    image_path = os.path.join(os.path.dirname(__file__), image_file)
    
    # Check if the image file exists
    if not os.path.exists(image_path):
        raise Exception(f"Image file not found: {image_path}")
    
    copied_routers = []
    
    for network in tollgate_networks:
        try:
            # Connect to the TollGate network
            connected_network = connect_to_network(network)
            
            # Get the router's IP address dynamically
            router_ip = get_router_ip()
            
            # Copy the image to the router
            copy_image_to_router(router_ip, image_path)
            copied_routers.append(router_ip)
            
        except Exception as e:
            print(f"Failed to copy image to network {network}: {e}")
            
    yield copied_routers
    
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


@pytest.fixture(scope="session")
def install_images_on_tollgates(tollgate_networks):
    """Find all TollGate networks, connect to each one, and flash the latest image."""
    if not tollgate_networks:
        raise Exception("No TollGate networks found")
    
    # Get the current network connection
    previous_connection = get_current_wifi_connection()
    print(f"Current network connection: {previous_connection}")
    
    # Path to the image file
    image_file = "8d84c36880d3957bdb9feda280902dc023438ac8c26ba3c10eb9177953f113c1.bin"
    image_path = os.path.join(os.path.dirname(__file__), image_file)
    
    # Check if the image file exists
    if not os.path.exists(image_path):
        raise Exception(f"Image file not found: {image_path}")
    
    flashed_routers = []
    
    for network in tollgate_networks:
        try:
            # Connect to the TollGate network
            connected_network = connect_to_network(network)
            
            # Get the router's IP address dynamically
            router_ip = get_router_ip()
            
            # Copy the image to the router
            copy_image_to_router(router_ip, image_path)
            
            # Flash the router with the image
            print(f"Flashing router at {router_ip} with image {image_path}")
            # Get the filename from the path
            image_filename = os.path.basename(image_path)
            remote_path = f"/tmp/{image_filename}"
            
            # Execute the sysupgrade command on the router
            print(f"Executing sysupgrade command on router {router_ip}...")
            sysupgrade_command = [
                "sshpass", "-p", ROUTER_PASSWORD,
                "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                f"root@{router_ip}",
                f"sysupgrade -n {remote_path}"
            ]
            
            print(f"Executing command: {' '.join(sysupgrade_command[:-1])} \"sysupgrade -n {remote_path}\"")
            # Execute the sysupgrade command without waiting for it to complete
            # since the router will reboot and break the SSH connection
            try:
                process = subprocess.Popen(sysupgrade_command, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                # Wait a short time to capture any immediate output
                stdout, stderr = process.communicate(timeout=10)
                stdout_output = stdout.decode() if isinstance(stdout, bytes) else str(stdout)
                stderr_output = stderr.decode() if isinstance(stderr, bytes) else str(stderr)
                
                # Print command output if there is any
                if stdout_output:
                    print(f"Command stdout: {stdout_output}")
                if stderr_output:
                    print(f"Command stderr: {stderr_output}")
                    
                # Check if we see upgrade messages, it means the flashing started successfully
                if "upgrade: Commencing upgrade" in stderr_output or "verifying sysupgrade tar file integrity" in stderr_output:
                    print(f"Router {router_ip} is rebooting with new firmware (this is expected)")
                    print(f"Flashing process initiated successfully on {router_ip}")
                    flashed_routers.append(router_ip)
                else:
                    print(f"Failed to execute sysupgrade command on {router_ip}")
                    print(f"Stdout: {stdout_output}")
                    print(f"Stderr: {stderr_output}")
                    print(f"Return code: {process.returncode}")
            except subprocess.TimeoutExpired:
                # This is expected - the router is rebooting and the connection is broken
                print(f"Router {router_ip} is rebooting with new firmware (this is expected)")
                print(f"Flashing process initiated successfully on {router_ip}")
                flashed_routers.append(router_ip)
                # Terminate the process since we don't need to wait for it
                process.terminate()
            except Exception as e:
                print(f"Error executing sysupgrade command on {router_ip}: {e}")
            
        except Exception as e:
            print(f"Failed to flash image to network {network}: {e}")
            
    yield flashed_routers
    
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


@pytest.fixture(scope="session")
def post_test_image_flasher(tollgate_networks):
    """Collect networks during tests and flash images after all tests complete."""
    if not tollgate_networks:
        raise Exception("No TollGate networks found")
    
    # Store the networks to flash later
    networks_to_flash = tollgate_networks.copy()
    
    # Get the current network connection
    previous_connection = get_current_wifi_connection()
    print(f"Current network connection: {previous_connection}")
    
    # Path to the image file
    image_file = "d702fbc5c69f2833539173fd1c5a138f77cf2b9854a12a2c10068ee5f8277097.bin"
    image_path = os.path.join(os.path.dirname(__file__), image_file)
    
    # Check if the image file exists
    if not os.path.exists(image_path):
        raise Exception(f"Image file not found: {image_path}")
    
    flashed_routers = []
    
    # This will run after all tests complete
    yield networks_to_flash
    
    # Flash images after all tests have completed
    print("Flashing images on tollgates after tests complete...")
    for network in networks_to_flash:
        try:
            # Connect to the TollGate network
            connected_network = connect_to_network(network)
            
            # Get the router's IP address dynamically
            router_ip = get_router_ip()
            
            # Copy the image to the router
            copy_image_to_router(router_ip, image_path)
            
            # Flash the router with the image
            print(f"Flashing router at {router_ip} with image {image_path}")
            # Get the filename from the path
            image_filename = os.path.basename(image_path)
            remote_path = f"/tmp/{image_filename}"
            
            # Execute the sysupgrade command on the router
            print(f"Executing sysupgrade command on router {router_ip}...")
            sysupgrade_command = [
                "sshpass", "-p", ROUTER_PASSWORD,
                "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                f"root@{router_ip}",
                f"sysupgrade -n {remote_path}"
            ]
            
            print(f"Executing command: {' '.join(sysupgrade_command[:-1])} \"sysupgrade -n {remote_path}\"")
            # Execute the sysupgrade command without waiting for it to complete
            # since the router will reboot and break the SSH connection
            try:
                process = subprocess.Popen(sysupgrade_command, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                # Wait a short time to capture any immediate output
                stdout, stderr = process.communicate(timeout=10)
                stdout_output = stdout.decode() if isinstance(stdout, bytes) else str(stdout)
                stderr_output = stderr.decode() if isinstance(stderr, bytes) else str(stderr)
                
                # Print command output if there is any
                if stdout_output:
                    print(f"Command stdout: {stdout_output}")
                if stderr_output:
                    print(f"Command stderr: {stderr_output}")
                    
                # Check if we see upgrade messages, it means the flashing started successfully
                if "upgrade: Commencing upgrade" in stderr_output or "verifying sysupgrade tar file integrity" in stderr_output:
                    print(f"Router {router_ip} is rebooting with new firmware (this is expected)")
                    print(f"Flashing process initiated successfully on {router_ip}")
                    flashed_routers.append(router_ip)
                else:
                    print(f"Failed to execute sysupgrade command on {router_ip}")
                    print(f"Stdout: {stdout_output}")
                    print(f"Stderr: {stderr_output}")
                    print(f"Return code: {process.returncode}")
            except subprocess.TimeoutExpired:
                # This is expected - the router is rebooting and the connection is broken
                print(f"Router {router_ip} is rebooting with new firmware (this is expected)")
                print(f"Flashing process initiated successfully on {router_ip}")
                flashed_routers.append(router_ip)
                # Terminate the process since we don't need to wait for it
                process.terminate()
            except Exception as e:
                print(f"Error executing sysupgrade command on {router_ip}: {e}")
            
        except Exception as e:
            print(f"Failed to flash image to network {network}: {e}")
            
    print(f"Successfully flashed routers: {flashed_routers}")
    
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

@pytest.fixture(scope="session")
def router_information():
    """Provide access to router information collected during network configuration tests."""
    return router_info