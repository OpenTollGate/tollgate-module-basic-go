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

@pytest.mark.order(1)
def test_connect_to_wifi_network(post_test_image_flasher, tollgate_networks):
    """Test that connects to a WiFi network."""
    print("Starting test_connect_to_wifi_network")

    # Connect to the first TollGate network to configure the first router
    if not post_test_image_flasher:
        pytest.skip("No networks found for testing")

    network = post_test_image_flasher[0]
    print(f"Connecting to network: {network}")

    try:
        subprocess.run(
            ["nmcli", "device", "wifi", "connect", network],
            check=True,
            capture_output=True,
            text=True
        )
        print(f"Successfully connected to network: {network}")
    except subprocess.CalledProcessError as e:
        pytest.fail(f"Failed to connect to network {network}: {e}")

    # Wait a bit for the connection to stabilize
    time.sleep(5)
    
    # Verify we're connected to the right network
    try:
        result = subprocess.run(
            ["nmcli", "-t", "-f", "name,device", "connection", "show", "--active"],
            check=True,
            capture_output=True,
            text=True
        )
        if network not in result.stdout:
            pytest.fail(f"Not connected to expected network {network}")
    except subprocess.CalledProcessError as e:
        pytest.fail(f"Failed to verify network connection: {e}")

@pytest.mark.order(2)
@pytest.mark.dependency(depends=["test_connect_to_wifi_network"])
def test_create_wwan_interface_and_get_router_ip():
    """Test that creates the wwan network interface if it doesn't exist and gets router IP."""
    print("Starting test_create_wwan_interface_and_get_router_ip")
    
    # Get the router IP
    router_ip = get_router_ip()
    if not router_ip:
        pytest.fail("Could not determine router IP address")
        
    print(f"Router IP: {router_ip}")
    
    router_password = "root"
    
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
                    pytest.fail(f"Failed to execute command '{cmd}': {result.stderr}")
                print(f"Executed: {cmd}")
        else:
            print("wwan interface already exists")
            
        # Store router IP for next test
        pytest.router_ip = router_ip
        
    except Exception as e:
        pytest.fail(f"Failed to create wwan interface: {e}")

@pytest.mark.order(3)
@pytest.mark.dependency(depends=["test_create_wwan_interface_and_get_router_ip"])
def test_configure_wireless_station_interfaces():
    """Test that configures the wireless station interfaces."""
    # Get router IP from previous test
    router_ip = getattr(pytest, 'router_ip', None)
    if not router_ip:
        pytest.skip("Router IP not available from previous test")
        
    router_password = "root"
    target_password = "c03rad0r123"
    
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
            pytest.fail(f"Failed to create wifinet0 interface: {result.stderr}")
        print(f"Creating wifinet0 interface result: {result.stdout}")
    
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
            pytest.fail(f"Failed to create wifinet1 interface: {result.stderr}")
        print(f"Creating wifinet1 interface result: {result.stdout}")
    
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
        pytest.fail(f"Failed to configure wireless interfaces: {result.stderr}")
    print(f"Successfully executed uci commands on router {router_ip}")
    
    # Store router IP for next test
    pytest.router_ip = router_ip

@pytest.mark.order(4)
@pytest.mark.dependency(depends=["test_configure_wireless_station_interfaces"])
def test_restart_network_and_verify_connectivity():
    """Test that restarts network services and verifies internet connectivity."""
    # Get router IP from previous test
    router_ip = getattr(pytest, 'router_ip', None)
    if not router_ip:
        pytest.skip("Router IP not available from previous test")
        
    router_password = "root"
    
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
        pytest.fail(f"Failed to restart network services: {result.stderr}")
    print("Network services restarted successfully")
    
    # Wait for network to stabilize after restart
    time.sleep(10)  # Give the network some time to come up
    
    # Verify internet connectivity
    if not verify_internet_connectivity(router_ip, router_password):
        pytest.fail("Router cannot connect to the internet after configuration")
    
    print(f"Router {router_ip} is successfully configured and connected to the internet")
    
    # Store router information for use by other tests
    pytest.router_ips = [router_ip]
    pytest.router_ssids = ["GL-MT6000-e50-5G"]  # This is the SSID we're connecting to