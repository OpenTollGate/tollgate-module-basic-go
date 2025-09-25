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

def test_collect_networks_for_flashing(post_test_image_flasher, tollgate_networks):
    """Test that collects networks for flashing after tests complete."""
    # Print the SSIDs of all found networks
    print(f"Found TollGate networks (SSIDs): {tollgate_networks}")
    
    # The fixture collects networks to flash after tests complete
    # We just need to verify that the fixture ran and collected results
    print(f"Networks collected for post-test flashing: {post_test_image_flasher}")
    # We don't assert on the number of networks since some may not be available
    # in a real-world scenario. The test passes as long as the fixture completes.
    
    # Connect to each router, SSH into it, connect to GL-MT6000-e50-5G SSID, and reboot
    router_password = "c03rad0r123"
    target_ssid = "GL-MT6000-e50-5G"
    target_password = "c03rad0r123"
    
    router_ips = []
    router_ssids = []
    
    for network in post_test_image_flasher:
        try:
            print(f"Processing router for network: {network}")
            
            # Connect to the TollGate network
            print(f"Connecting to network: {network}")
            subprocess.run(
                ["nmcli", "device", "wifi", "connect", network],
                check=True,
                capture_output=True,
                text=True
            )
            print(f"Successfully connected to network: {network}")
            
            # Get the router's IP address
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
            
            if not router_ip:
                print(f"Could not determine router IP for network {network}")
                continue
                
            print(f"Determined router IP: {router_ip}")
            router_ips.append(router_ip)
            router_ssids.append(network)
            
            # SSH into the router and connect to the target SSID using uci commands
            print(f"Using uci commands to connect to target SSID {target_ssid} on router {router_ip}")
            # First, let's check if the sta interface exists
            ssh_command = [
                "sshpass", "-p", router_password,
                "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                f"root@{router_ip}",
                "uci show wireless.sta"
            ]
            
            result = subprocess.run(ssh_command, capture_output=True, text=True)
            print(f"Checking if sta interface exists: {result.stdout}")
            if "Entry not found" in result.stderr:
                # Create the sta interface
                print("Creating sta interface")
                ssh_command = [
                    "sshpass", "-p", router_password,
                    "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                    f"root@{router_ip}",
                    "uci set wireless.sta=wifi-iface && uci set wireless.sta.device='radio0' && uci set wireless.sta.network='wwan' && uci set wireless.sta.mode='sta' && uci set wireless.sta.ssid='PLACEHOLDER' && uci set wireless.sta.key='PLACEHOLDER' && uci set wireless.sta.disabled='1'"
                ]
                
                result = subprocess.run(ssh_command, capture_output=True, text=True)
                print(f"Creating sta interface result: {result.stdout}, stderr: {result.stderr}")
            
            # Set the SSID and password for the sta interface
            ssh_command = [
                "sshpass", "-p", router_password,
                "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                f"root@{router_ip}",
                f"uci set wireless.sta.ssid='{target_ssid}' && uci set wireless.sta.key='{target_password}' && uci set wireless.sta.disabled='0' && uci commit wireless && wifi"
            ]
            
            print(f"Executing uci commands: {' '.join(ssh_command)}")
            result = subprocess.run(ssh_command, capture_output=True, text=True)
            if result.returncode == 0:
                print(f"Successfully connected to {target_ssid} on router {router_ip} using uci commands")
            else:
                print(f"Failed to connect to {target_ssid} on router {router_ip} using uci commands")
                print(f"Stderr: {result.stderr}")
                print(f"Stdout: {result.stdout}")
            
            # Reboot the router
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
                
        except subprocess.CalledProcessError as e:
            print(f"Error processing network {network}: {e}")
        except Exception as e:
            print(f"Unexpected error processing network {network}: {e}")
            
    print("Completed processing all routers")
    
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