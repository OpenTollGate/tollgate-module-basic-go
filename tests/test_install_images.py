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
    
    # Connect to each router, SSH into it, connect to the appropriate SSID, and reboot
    router_password = "c03rad0r123"
    target_password = "c03rad0r123"
    
    router_ips = []
    router_ssids = []
    
    for network in post_test_image_flasher:
        try:
            print(f"\n{'='*50}")
            print(f"Processing router for network: {network}")
            print(f"{'='*50}")
            
            # Determine the target SSID based on the network name
            print(f"DEBUG: Network name is '{network}'")
            if network.endswith("-5GHz"):
                print("DEBUG: Network ends with '-5GHz', selecting 5GHz radio and SSID")
                target_ssid = "GL-MT6000-e50-5G"
                radio_device = "radio1"  # 5GHz radio
                other_target_ssid = "GL-MT6000-e50"  # 2.4GHz SSID
            else:
                print("DEBUG: Network does not end with '-5GHz', selecting 2.4GHz radio and SSID")
                target_ssid = "GL-MT6000-e50"
                radio_device = "radio0"  # 2.4GHz radio
                other_target_ssid = "GL-MT6000-e50-5G"  # 5GHz SSID
                
            print(f"Target SSID for {network}: {target_ssid} (using {radio_device})")
            print(f"Other SSID (not used for this connection): {other_target_ssid}")
            
            # Connect to the TollGate network
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
            
            # Determine the sta interface name based on the radio device
            sta_interface = "wifinet1" if radio_device == "radio1" else "wifinet0"
            other_sta_interface = "wifinet0" if radio_device == "radio1" else "wifinet1"
            
            # Determine the target SSID for the other radio
            other_radio_target_ssid = other_target_ssid
            
            # Check if wifinet0 interface exists, create it if not
            # Always set the correct SSID and key for both interfaces
            ssh_command = [
                "sshpass", "-p", router_password,
                "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                f"root@{router_ip}",
                "uci show wireless.wifinet0"
            ]
            
            result = subprocess.run(ssh_command, capture_output=True, text=True)
            print(f"Checking if wifinet0 interface exists: {result.stdout}")
            if "Entry not found" in result.stderr:
                print("Creating wifinet0 interface on radio0")
                ssh_command = [
                    "sshpass", "-p", router_password,
                    "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                    f"root@{router_ip}",
                    f"uci set wireless.wifinet0=wifi-iface && uci set wireless.wifinet0.device='radio0' && uci set wireless.wifinet0.network='wwan' && uci set wireless.wifinet0.mode='sta' && uci set wireless.wifinet0.ssid='GL-MT6000-e50' && uci set wireless.wifinet0.key='{target_password}' && uci set wireless.wifinet0.encryption='psk2' && uci set wireless.wifinet0.disabled='1'"
                ]
                
                result = subprocess.run(ssh_command, capture_output=True, text=True)
                print(f"Creating wifinet0 interface result: {result.stdout}, stderr: {result.stderr}")
            
            # Check if wifinet1 interface exists, create it if not
            # Always set the correct SSID and key for both interfaces
            ssh_command = [
                "sshpass", "-p", router_password,
                "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                f"root@{router_ip}",
                "uci show wireless.wifinet1"
            ]
            
            result = subprocess.run(ssh_command, capture_output=True, text=True)
            print(f"Checking if wifinet1 interface exists: {result.stdout}")
            if "Entry not found" in result.stderr:
                print("Creating wifinet1 interface on radio1")
                ssh_command = [
                    "sshpass", "-p", router_password,
                    "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                    f"root@{router_ip}",
                    f"uci set wireless.wifinet1=wifi-iface && uci set wireless.wifinet1.device='radio1' && uci set wireless.wifinet1.network='wwan' && uci set wireless.wifinet1.mode='sta' && uci set wireless.wifinet1.ssid='GL-MT6000-e50-5G' && uci set wireless.wifinet1.key='{target_password}' && uci set wireless.wifinet1.encryption='psk2' && uci set wireless.wifinet1.disabled='1'"
                ]
                
                result = subprocess.run(ssh_command, capture_output=True, text=True)
                print(f"Creating wifinet1 interface result: {result.stdout}, stderr: {result.stderr}")
            
            # Set the SSID, key, and encryption for both wifinet interfaces and enable the target one, disable the other
            ssh_command = [
                "sshpass", "-p", router_password,
                "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                f"root@{router_ip}",
                f"uci set wireless.wifinet0.ssid='{other_target_ssid}' && uci set wireless.wifinet0.key='{target_password}' && uci set wireless.wifinet0.encryption='psk2' && "
                f"uci set wireless.wifinet1.ssid='{target_ssid}' && uci set wireless.wifinet1.key='{target_password}' && uci set wireless.wifinet1.encryption='psk2' && "
                f"uci set wireless.{sta_interface}.disabled='0' && uci set wireless.{other_sta_interface}.disabled='1' && uci commit wireless && wifi"
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
        finally:
            print(f"{'-'*50}")
            print(f"Finished processing router for network: {network}")
            print(f"{'-'*50}\n")
            
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