import pytest
import subprocess
import time

def wait_for_router_reboot(router_ip, router_ssid, timeout=300):
    """Wait for the router to come back online after reboot."""
    print(f"Waiting for router {router_ssid} ({router_ip}) to come back online...")
    start_time = time.time()
    
    while time.time() - start_time < timeout:
        try:
            # Try to ping the router
            result = subprocess.run(
                ["ping", "-c", "1", "-W", "5", router_ip],
                capture_output=True,
                text=True
            )
            
            if result.returncode == 0:
                print(f"Router {router_ssid} ({router_ip}) is back online")
                return True
        except Exception as e:
            pass
        
        time.sleep(10)
    
    print(f"Router {router_ssid} ({router_ip}) did not come back online within {timeout} seconds")
    return False

@pytest.mark.order(4)
def test_reboot_routers(post_test_image_flasher, tollgate_networks):
    """Test that reboots routers after configuring upstream gateways."""
    # This test depends on the network configuration tests having run successfully
    # We'll use the router IP from the network configuration tests
    if not hasattr(pytest, 'router_ips') or not hasattr(pytest, 'router_ssids'):
        pytest.skip("Router IPs and SSIDs not available from network configuration tests")
    
    router_ips = pytest.router_ips
    router_ssids = pytest.router_ssids
    
    # Reboot all routers
    router_password = "c03rad0r123"
    for i, router_ip in enumerate(router_ips):
        try:
            print(f"Rebooting router {router_ip}")
            ssh_command = [
                "sshpass", "-p", router_password,
                "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                f"root@{router_ip}",
                "reboot"
            ]
            
            result = subprocess.run(ssh_command, capture_output=True, text=True)
            if result.returncode == 0:
                print(f"Successfully rebooted router {router_ip}")
            else:
                # Reboot command will disconnect the SSH session, so this is expected to fail
                print(f"Router {router_ip} is rebooting (this is expected)")
        except Exception as e:
            print(f"Error rebooting router {router_ip}: {e}")
    
    # Wait for all routers to come back online
    print("Waiting for all routers to come back online...")
    for i, router_ip in enumerate(router_ips):
        if not wait_for_router_reboot(router_ip, router_ssids[i]):
            print(f"Router {router_ssids[i]} ({router_ip}) did not come back online")
    
    # Prompt the user for input
    input("Please press Enter after verifying that all routers have restarted and connected to the new network...")
    
    # Get new IP addresses for all routers
    print("Getting new IP addresses for all routers...")
    new_router_ips = []
    for network in post_test_image_flasher:
        try:
            print(f"Connecting to network: {network}")
            subprocess.run(
                ["nmcli", "device", "wifi", "connect", network],
                check=True,
                capture_output=True,
                text=True
            )
            print(f"Successfully connected to network: {network}")
            
            # Wait for the network to be stable
            print("Waiting for network to be stable...")
            max_wait_time = 30  # seconds
            wait_interval = 2   # seconds
            start_time = time.time()
            network_stable = False
            
            while time.time() - start_time < max_wait_time:
                try:
                    # Try to get the route
                    result = subprocess.run(
                        ["ip", "route", "get", "1.1.1.1"],
                        capture_output=True,
                        text=True,
                        check=True
                    )
                    
                    # If we get here, the network is stable
                    network_stable = True
                    print("Network is stable.")
                    break
                except subprocess.CalledProcessError:
                    # Network is not ready yet, wait and try again
                    print("Network not ready, waiting...")
                    time.sleep(wait_interval)
            
            if not network_stable:
                print(f"Network did not become stable within {max_wait_time} seconds")
                continue
            
            # The network stability check has already verified the route, so we can proceed
            # Get the router's new IP address
            result = subprocess.run(
                ["ip", "route", "get", "1.1.1.1"],
                capture_output=True,
                text=True,
                check=True
            )
            
            new_router_ip = None
            for line in result.stdout.split('\n'):
                if "via" in line:
                    parts = line.split()
                    for i, part in enumerate(parts):
                        if part == "via":
                            new_router_ip = parts[i + 1]
                            break
                    if new_router_ip:
                        break
            
            if not new_router_ip:
                print(f"Could not determine new router IP for network {network}")
                continue
                
            print(f"Determined new router IP: {new_router_ip}")
            new_router_ips.append(new_router_ip)
        except subprocess.CalledProcessError as e:
            print(f"Error connecting to network {network}: {e}")
        except Exception as e:
            print(f"Unexpected error connecting to network {network}: {e}")
    
    # Print the new IP addresses
    print(f"New router IP addresses: {new_router_ips}")