#!/usr/bin/env python3
"""
Script to monitor an ethernet interface for connected routers and flash them with an image.

This script continuously monitors the specified ethernet interface for IP address changes.
When a router is detected (IP address assigned), it flashes the router with the image.
When the network cable is unplugged, it returns to monitoring mode.
"""

import subprocess
import time
import os
import sys

# Configuration
INTERFACE = "enx00e04c683d2d"
ROUTER_PASSWORD = "c03rad0r123"
IMAGE_FILE = "5ccecb2bdc0e613e2b8315f398e6514ccd49ac2ab90b77e970a0a25ceb8c7e8c.bin"


def get_interface_ip(interface):
    """
    Get the IP address assigned to the specified interface.
    
    Args:
        interface (str): The name of the interface to check
        
    Returns:
        str or None: The IP address if assigned, None otherwise
    """
    try:
        result = subprocess.run(
            ["ip", "addr", "show", interface],
            capture_output=True,
            text=True,
            check=True
        )
        
        # Parse the output to find the IP address
        for line in result.stdout.split('\n'):
            if 'inet ' in line and '127.0.0.1' not in line:
                # Extract IP address (before / subnet mask)
                ip_address = line.split()[1].split('/')[0]
                return ip_address
                
        return None
    except subprocess.CalledProcessError:
        return None


def get_router_ip(interface):
    """
    Get the router's IP address for the current network.
    
    Args:
        interface (str): The name of the interface to check
        
    Returns:
        str: The router's IP address
    """
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
                        return router_ip
                        
        raise Exception("Could not find gateway in ip route output")
    except (subprocess.CalledProcessError, ValueError, IndexError) as e:
        raise Exception(f"Failed to determine router IP: {e}")


def copy_image_to_router(ip_address, image_path):
    """
    Copy an image file to the router's /tmp directory using ssh and cat.
    
    Args:
        ip_address (str): The IP address of the router
        image_path (str): The path to the image file
        
    Raises:
        Exception: If the copy command fails
    """
    try:
        print(f"Copying image to router at {ip_address}...")
        # Use ssh with cat to copy the image file to the router's /tmp directory
        # This approach works better with embedded systems that don't have sftp-server
        
        # Get the filename from the path
        image_filename = os.path.basename(image_path)
        remote_path = f"/tmp/{image_filename}"
        
        # Read the local file and send it through ssh
        with open(image_path, 'rb') as f:
            ssh_command = [
                "sshpass", "-p", ROUTER_PASSWORD,
                "ssh", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=10",
                f"root@{ip_address}",
                f"cat > {remote_path}"
            ]
            
            print(f"Executing command: {' '.join(ssh_command[:-1])} \"cat > {remote_path}\"")
            print(f"Sending file: {image_path} ({os.path.getsize(image_path)} bytes)")
            
            result = subprocess.run(ssh_command, input=f.read(), capture_output=True, check=True)
            
            # Print command output if there is any
            if result.stdout:
                print(f"Command stdout: {result.stdout.decode()}")
            if result.stderr:
                print(f"Command stderr: {result.stderr.decode()}")
            
        print(f"Image copied successfully to {ip_address}:{remote_path}")
    except subprocess.CalledProcessError as e:
        raise Exception(f"Failed to copy image to {ip_address}:/tmp/: {e}\nStdout: {e.stdout}\nStderr: {e.stderr}")
    except IOError as e:
        raise Exception(f"Failed to read image file {image_path}: {e}")


def flash_router(router_ip, image_path):
    """
    Flash the router with the image.
    
    Args:
        router_ip (str): The IP address of the router
        image_path (str): The path to the image file
    """
    try:
        print(f"Flashing router at {router_ip} with image {image_path}")
        copy_image_to_router(router_ip, image_path)
        
        # Get the filename from the path
        image_filename = os.path.basename(image_path)
        remote_path = f"/tmp/{image_filename}"
        
        # Execute the sysupgrade command on the router
        print(f"Executing sysupgrade command on router {router_ip}...")
        sysupgrade_command = [
            "sshpass", "-p", ROUTER_PASSWORD,
            "ssh", "-o", "StrictHostKeyChecking=no", "-o", "ConnectTimeout=10",
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
            else:
                print(f"Failed to execute sysupgrade command on {router_ip}")
                print(f"Stdout: {stdout_output}")
                print(f"Stderr: {stderr_output}")
                print(f"Return code: {process.returncode}")
        except subprocess.TimeoutExpired:
            # This is expected - the router is rebooting and the connection is broken
            print(f"Router {router_ip} is rebooting with new firmware (this is expected)")
            print(f"Flashing process initiated successfully on {router_ip}")
            # Terminate the process since we don't need to wait for it
            process.terminate()
        except Exception as e:
            print(f"Error executing sysupgrade command on {router_ip}: {e}")
    except Exception as e:
        print(f"Failed to flash router at {router_ip}: {e}")


def main():
    """Main monitoring loop."""
    print(f"Monitoring interface {INTERFACE} for router connections...")
    
    # Get the path to the image file (should be in the same directory as this script)
    script_dir = os.path.dirname(os.path.abspath(__file__))
    image_path = os.path.join(script_dir, IMAGE_FILE)
    
    # Check if the image file exists
    if not os.path.exists(image_path):
        print(f"Error: Image file not found: {image_path}")
        sys.exit(1)
    
    print(f"Using image file: {image_path}")
    
    previous_ip = None
    router_flashed = False
    
    while True:
        try:
            # Get the current IP address of the interface
            current_ip = get_interface_ip(INTERFACE)
            
            # Check if the IP address has changed
            if current_ip != previous_ip:
                if current_ip is None:
                    print(f"Interface {INTERFACE} is down or has no IP address")
                    router_flashed = False
                else:
                    print(f"Interface {INTERFACE} has IP address: {current_ip}")
                    try:
                        # Get the router's IP address
                        router_ip = get_router_ip(INTERFACE)
                        print(f"Router detected at IP: {router_ip}")
                        
                        # Flash the router if not already flashed
                        if not router_flashed:
                            flash_router(router_ip, image_path)
                            router_flashed = True
                        else:
                            print("Router already flashed, skipping...")
                            
                    except Exception as e:
                        print(f"Error getting router IP or flashing router: {e}")
            
            previous_ip = current_ip
            
            # Wait before checking again
            time.sleep(2)
            
        except KeyboardInterrupt:
            print("\nMonitoring stopped by user")
            break
        except Exception as e:
            print(f"Error in monitoring loop: {e}")
            time.sleep(5)  # Wait a bit longer on error


if __name__ == "__main__":
    main()