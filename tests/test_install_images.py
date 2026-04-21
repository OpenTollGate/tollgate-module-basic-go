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
    
    # We don't need to reconnect to networks since we're already connected
    # Just get the current router IP for each network
    try:
        # Get the current default route
        result = subprocess.run(
            ["ip", "route", "get", "1.1.1.1"],
            capture_output=True,
            text=True,
            check=True
        )
        
        current_router_ip = None
        for line in result.stdout.split('\n'):
            if "via" in line:
                parts = line.split()
                for i, part in enumerate(parts):
                    if part == "via":
                        current_router_ip = parts[i + 1]
                        break
                if current_router_ip:
                    break
        
        if current_router_ip:
            print(f"Current router IP: {current_router_ip}")
            new_router_ips.append(current_router_ip)
        else:
            print("Could not determine current router IP")
    except subprocess.CalledProcessError as e:
        print(f"Error getting current router IP: {e}")
    except Exception as e:
        print(f"Unexpected error getting current router IP: {e}")
    
    # Print the new IP addresses
    print(f"New router IP addresses: {new_router_ips}")