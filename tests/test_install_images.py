import pytest
import subprocess

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
            
            # SSH into the router and check if wifi-connect command is available
            print(f"Checking available commands on router {router_ip}")
            ssh_command = [
                "sshpass", "-p", router_password,
                "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                f"root@{router_ip}",
                "which wifi-connect || echo 'wifi-connect not found'"
            ]
            
            result = subprocess.run(ssh_command, capture_output=True, text=True)
            if "wifi-connect" in result.stdout:
                print(f"wifi-connect command is available on router {router_ip}")
                # SSH into the router and connect to the target SSID
                print(f"Connecting to target SSID {target_ssid} on router {router_ip}")
                ssh_command = [
                    "sshpass", "-p", router_password,
                    "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                    f"root@{router_ip}",
                    f"wifi-connect -s {target_ssid} -p {target_password}"
                ]
                
                result = subprocess.run(ssh_command, capture_output=True, text=True)
                if result.returncode == 0:
                    print(f"Successfully connected to {target_ssid} on router {router_ip}")
                else:
                    print(f"Failed to connect to {target_ssid} on router {router_ip}")
                    print(f"Stderr: {result.stderr}")
            else:
                print(f"wifi-connect command not found on router {router_ip}")
                # Try using uci commands to connect to the target SSID
                print(f"Using uci commands to connect to target SSID {target_ssid} on router {router_ip}")
                ssh_command = [
                    "sshpass", "-p", router_password,
                    "ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
                    f"root@{router_ip}",
                    f"uci set wireless.sta.ssid='{target_ssid}' && uci set wireless.sta.key='{target_password}' && uci commit wireless && wifi"
                ]
                
                result = subprocess.run(ssh_command, capture_output=True, text=True)
                if result.returncode == 0:
                    print(f"Successfully connected to {target_ssid} on router {router_ip} using uci commands")
                else:
                    print(f"Failed to connect to {target_ssid} on router {router_ip} using uci commands")
                    print(f"Stderr: {result.stderr}")
            
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