import pytest
import subprocess
import time
import re
import socket

def wait_for_network_stability(router_ip, router_password, timeout=30):
    """Wait for the network to become stable after configuration changes."""
    print(f"Waiting for network stability on router {router_ip}...")
    start_time = time.time()
    
    while time.time() - start_time < timeout:
        try:
            # Try to get the route
            ssh_command = [
                "sshpass", "-p", router_password,
                "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                f"root@{router_ip}",
                "ip route get 1.1.1.1"
            ]
            
            result = subprocess.run(ssh_command, capture_output=True, text=True, check=True)
            
            # If we get here, the network is stable
            print("Network is stable.")
            return True
        except subprocess.CalledProcessError:
            # Network is not ready yet, wait and try again
            print("Network not ready, waiting...")
            time.sleep(2)
    
    print(f"Network did not become stable within {timeout} seconds")
    return False

def verify_internet_connectivity(router_ip, router_password, timeout=30):
    """Verify that the router can connect to the internet."""
    print(f"Verifying internet connectivity on router {router_ip}...")
    start_time = time.time()
    
    while time.time() - start_time < timeout:
        try:
            ssh_command = [
                "sshpass", "-p", router_password,
                "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                f"root@{router_ip}",
                "ping -c 1 8.8.8.8"
            ]
            
            result = subprocess.run(ssh_command, capture_output=True, text=True, check=True)
            if result.returncode == 0:
                print("Internet connectivity verified successfully")
                return True
        except subprocess.CalledProcessError:
            # Internet not accessible yet, wait and try again
            print("Internet not accessible, waiting...")
            time.sleep(2)
    
    print(f"Internet connectivity not established within {timeout} seconds")
    return False

def get_router_ip():
    """Get the IP address of the router we're currently connected to."""
    try:
        # Get the gateway IP
        result = subprocess.run(
            ["ip", "route", "get", "1.1.1.1"],
            capture_output=True,
            text=True,
            check=True
        )
        
        router_ip = None
        for line in result.stdout.split('\n'):
            if "via" in line:
                parts = line.split()
                for i, part in enumerate(parts):
                    if part == "via":
                        router_ip = parts[i + 1]
                        break
                if router_ip:
                    break
        
        return router_ip
    except subprocess.CalledProcessError as e:
        print(f"Failed to determine router IP: {e}")
        return None

def connect_to_network(ssid):
    """Connect to a WiFi network with the given SSID."""
    max_attempts = 10
    attempt = 0
    
    while attempt < max_attempts:
        try:
            # Disconnect from current network
            subprocess.run(
                ["nmcli", "device", "disconnect", "wlp59s0"],
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

def create_wwan_interface(router_ip, router_password="root"):
    """Create the wwan network interface if it doesn't exist."""
    try:
        # Check if wwan interface exists
        ssh_command = [
            "sshpass", "-p", router_password,
            "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
            f"root@{router_ip}", "/sbin/uci get network.wwan"
        ]
        result = subprocess.run(ssh_command, capture_output=True, text=True)
        
        if "Entry not found" in result.stderr:
            print("wwan interface does not exist, creating it...")
            # Create wwan interface
            create_commands = [
                "/sbin/uci set network.wwan=interface",
                "/sbin/uci set network.wwan.proto='dhcp'",
                "/sbin/uci set network.wwan.metric='2048'",
                "/sbin/uci commit network"
            ]
            
            for cmd in create_commands:
                ssh_command = [
                    "sshpass", "-p", router_password,
                    "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                    f"root@{router_ip}", cmd
                ]
                result = subprocess.run(ssh_command, capture_output=True, text=True)
                if result.returncode != 0:
                    raise Exception(f"Failed to execute command '{cmd}': {result.stderr}")
                print(f"Executed: {cmd}")
        else:
            print("wwan interface already exists")
            
    except Exception as e:
        raise Exception(f"Failed to create wwan interface: {e}")

def configure_wireless_station_interfaces(router_ip, router_password="root", target_password="c03rad0r123"):
    """Configure the wireless station interfaces (wifinet0 and wifinet1)."""
    # Add router information for better output formatting
    print(f"=== Configuring Router at {router_ip} ===")
    
    # Configure both 2.4GHz and 5GHz wireless interfaces
    print("Configuring wireless station interfaces")
    
    # Check if wifinet0 interface exists, create it if not
    ssh_command = [
        "sshpass", "-p", router_password,
        "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
        f"root@{router_ip}", "/sbin/uci show wireless.wifinet0"
    ]
    
    result = subprocess.run(ssh_command, capture_output=True, text=True)
    print(f"Checking if wifinet0 interface exists: {result.stdout}")
    if "Entry not found" in result.stderr:
        print("Creating wifinet0 interface on radio0")
        ssh_command = [
            "sshpass", "-p", router_password,
            "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
            f"root@{router_ip}",
            f"/sbin/uci set wireless.wifinet0=wifi-iface && /sbin/uci set wireless.wifinet0.device='radio0' && /sbin/uci set wireless.wifinet0.network='wwan' && /sbin/uci set wireless.wifinet0.mode='sta' && /sbin/uci set wireless.wifinet0.ssid='GL-MT6000-e50' && /sbin/uci set wireless.wifinet0.key='{target_password}' && /sbin/uci set wireless.wifinet0.encryption='psk2' && /sbin/uci set wireless.wifinet0.disabled='1'"
        ]
        
        result = subprocess.run(ssh_command, capture_output=True, text=True)
        if result.returncode != 0:
            raise Exception(f"Failed to create wifinet0 interface: {result.stderr}")
        print(f"Creating wifinet0 interface result: {result.stdout}")
    else:
        print("wifinet0 interface already exists")
    
    # Check if wifinet1 interface exists, create it if not
    ssh_command = [
        "sshpass", "-p", router_password,
        "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
        f"root@{router_ip}", "/sbin/uci show wireless.wifinet1"
    ]
    
    result = subprocess.run(ssh_command, capture_output=True, text=True)
    print(f"Checking if wifinet1 interface exists: {result.stdout}")
    if "Entry not found" in result.stderr:
        print("Creating wifinet1 interface on radio1")
        ssh_command = [
            "sshpass", "-p", router_password,
            "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
            f"root@{router_ip}",
            f"/sbin/uci set wireless.wifinet1=wifi-iface && /sbin/uci set wireless.wifinet1.device='radio1' && /sbin/uci set wireless.wifinet1.network='wwan' && /sbin/uci set wireless.wifinet1.mode='sta' && /sbin/uci set wireless.wifinet1.ssid='GL-MT6000-e50-5G' && /sbin/uci set wireless.wifinet1.key='{target_password}' && /sbin/uci set wireless.wifinet1.encryption='psk2' && /sbin/uci set wireless.wifinet1.disabled='1'"
        ]
        
        result = subprocess.run(ssh_command, capture_output=True, text=True)
        if result.returncode != 0:
            raise Exception(f"Failed to create wifinet1 interface: {result.stderr}")
        print(f"Creating wifinet1 interface result: {result.stdout}")
    else:
        print("wifinet1 interface already exists")
    
    # Set the SSID, key, and encryption for both wifinet interfaces
    # Enable 5GHz (wifinet1) and disable 2.4GHz (wifinet0) for this test
    ssh_command = [
        "sshpass", "-p", router_password,
        "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
        f"root@{router_ip}",
        f"/sbin/uci set wireless.wifinet0.ssid='GL-MT6000-e50' && /sbin/uci set wireless.wifinet0.key='{target_password}' && /sbin/uci set wireless.wifinet0.encryption='psk2' && /sbin/uci set wireless.wifinet0.disabled='1' && "
        f"/sbin/uci set wireless.wifinet1.ssid='GL-MT6000-e50-5G' && /sbin/uci set wireless.wifinet1.key='{target_password}' && /sbin/uci set wireless.wifinet1.encryption='psk2' && /sbin/uci set wireless.wifinet1.disabled='0' && "
        f"/sbin/uci commit wireless"
    ]
    
    print(f"Executing uci commands: {' '.join(ssh_command)}")
    result = subprocess.run(ssh_command, capture_output=True, text=True)
    if result.returncode != 0:
        raise Exception(f"Failed to configure wireless interfaces: {result.stderr}")
    print(f"Successfully executed uci commands on router {router_ip}")

def restart_network_services(router_ip, router_password="root"):
    """Restart network services and verify internet connectivity."""
    # Add router information for better output formatting
    print(f"=== Restarting Network Services on Router at {router_ip} ===")
    
    # Restart network services to apply changes
    print("Restarting network services...")
    ssh_command = [
        "sshpass", "-p", router_password,
        "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
        f"root@{router_ip}",
        "/etc/init.d/network restart"
    ]
    
    result = subprocess.run(ssh_command, capture_output=True, text=True)
    if result.returncode != 0:
        raise Exception(f"Failed to restart network services: {result.stderr}")
    print("Network services restarted successfully")
    
    # Wait for network to stabilize after restart
    time.sleep(10)  # Give the network some time to come up
    
    # Verify internet connectivity
    if not verify_internet_connectivity(router_ip, router_password):
        raise Exception("Router cannot connect to the internet after configuration")
    
    print(f"Router {router_ip} is successfully configured and connected to the internet")

@pytest.mark.order(1)
def test_configure_all_routers(request, post_test_image_flasher, tollgate_networks):
    """Test that configures all routers with upstream gateways."""
    print("Starting test_configure_all_routers")
    
    # Check if we have any networks to configure
    if not post_test_image_flasher:
        pytest.skip("No networks found for testing")
    
    # Store information about all configured routers
    configured_routers = []
    configured_ssids = []
    
    # Configure each router
    for i, network in enumerate(post_test_image_flasher):
        try:
            print(f"\n=== Configuring Router {i+1}/{len(post_test_image_flasher)}: {network} ===")
            
            # Connect to the network
            print(f"Connecting to network: {network}")
            connect_to_network(network)
            
            # Wait a bit for the connection to stabilize
            time.sleep(5)
            
            # Get the router IP
            router_ip = get_router_ip()
            if not router_ip:
                print(f"Could not determine router IP for network {network}, skipping...")
                continue
                
            print(f"Router IP: {router_ip}")
            
            # Create wwan interface if it doesn't exist
            create_wwan_interface(router_ip)
            
            # Configure wireless station interfaces
            configure_wireless_station_interfaces(router_ip)
            
            # Restart network services and verify connectivity
            restart_network_services(router_ip)
            
            # Store information about this router
            configured_routers.append(router_ip)
            configured_ssids.append(network)
            
            # Reboot the router immediately after configuration while still connected to its network
            print(f"Rebooting router {router_ip}...")
            router_password = "c03rad0r123"
            ssh_command = [
                "sshpass", "-p", router_password,
                "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                f"root@{router_ip}",
                "reboot"
            ]
            
            try:
                result = subprocess.run(ssh_command, capture_output=True, text=True, timeout=10)
                if result.returncode == 0:
                    print(f"Successfully sent reboot command to router {router_ip}")
                else:
                    # Reboot command will disconnect the SSH session, so this is expected to fail
                    print(f"Router {router_ip} is rebooting (this is expected)")
            except subprocess.TimeoutExpired:
                # This is expected as the reboot command will disconnect the SSH session
                print(f"Router {router_ip} is rebooting (this is expected)")
            except Exception as e:
                print(f"Error rebooting router {router_ip}: {e}")
            
        except Exception as e:
            print(f"Failed to configure router for network {network}: {e}")
            # Continue with the next router instead of failing the entire test
            continue
    
    # Wait for all routers to come back online and their SSIDs to reappear
    print("\n=== Waiting for routers to come back online and SSIDs to reappear ===")
    # First, wait a bit for routers to actually start rebooting
    time.sleep(30)
    
    available_networks = []
    max_wait_time = 300  # 5 minutes
    wait_interval = 10   # 10 seconds
    
    # Create a list of possible SSID patterns for each router
    ssid_patterns = []
    for router_ip, ssid in zip(configured_routers, configured_ssids):
        # For TollGate-* SSIDs, also check for SafeMode-TollGate-* and vice versa
        if ssid.startswith("TollGate-"):
            pattern = ssid.replace("TollGate-", "SafeMode-TollGate-", 1)
            ssid_patterns.append((router_ip, ssid, pattern))
        elif ssid.startswith("SafeMode-TollGate-"):
            pattern = ssid.replace("SafeMode-TollGate-", "TollGate-", 1)
            ssid_patterns.append((router_ip, ssid, pattern))
        else:
            ssid_patterns.append((router_ip, ssid, ssid))  # Fallback to same SSID
    
    start_time = time.time()
    while time.time() - start_time < max_wait_time:
        # Scan for available networks
        try:
            result = subprocess.run(
                ["nmcli", "device", "wifi", "list"],
                capture_output=True,
                text=True,
                check=True
            )
            
            # Check if our routers' SSIDs are in the list and we can actually connect
            for router_ip, original_ssid, alternate_ssid in ssid_patterns:
                # Check for both original and alternate SSID patterns
                found_ssid = None
                if original_ssid in result.stdout and original_ssid not in available_networks:
                    found_ssid = original_ssid
                elif alternate_ssid in result.stdout and alternate_ssid not in available_networks:
                    found_ssid = alternate_ssid
                
                if found_ssid:
                    print(f"SSID {found_ssid} is now available, attempting to connect...")
                    # Try to connect to verify it's actually back online
                    try:
                        connect_to_network(found_ssid)
                        print(f"Successfully connected to {found_ssid}")
                        available_networks.append(found_ssid)
                        # Disconnect to continue checking other networks
                        subprocess.run(
                            ["nmcli", "device", "disconnect", "wlp59s0"],
                            stderr=subprocess.DEVNULL
                        )
                    except Exception as e:
                        print(f"Failed to connect to {found_ssid}, router may not be fully back online yet: {e}")
            
            # If all routers are back, we're done
            if len(available_networks) == len(configured_routers):
                print("All routers are back online and their SSIDs are available")
                break
        except subprocess.CalledProcessError as e:
            print(f"Error scanning for networks: {e}")
        
        # Wait before checking again
        print(f"Waiting for {len(configured_routers) - len(available_networks)} routers to come back online...")
        time.sleep(wait_interval)
    
    if len(available_networks) < len(configured_routers):
        print(f"Warning: Only {len(available_networks)} out of {len(configured_routers)} routers came back online")
    
    # Reconnect to the first available network to continue with other tests
    if available_networks:
        print(f"\n=== Reconnecting to network {available_networks[0]} ===")
        try:
            connect_to_network(available_networks[0])
            print(f"Successfully reconnected to network {available_networks[0]}")
        except Exception as e:
            print(f"Failed to reconnect to network {available_networks[0]}: {e}")
    
    # Store router information for use by other tests
    pytest.router_ips = configured_routers
    pytest.router_ssids = configured_ssids
    
    # Check if we configured at least one router
    if not configured_routers:
        pytest.fail("Failed to configure any routers")
    
    print(f"\n=== Successfully configured {len(configured_routers)} routers ===")
    for i, (ip, ssid) in enumerate(zip(configured_routers, configured_ssids)):
        print(f"  Router {i+1}: {ssid} ({ip})")
    
    # Mark this test as passed for dependency tracking
    request.node.user_properties.append(("test_configure_all_routers", "passed"))