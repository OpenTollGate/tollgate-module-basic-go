import pytest
import subprocess
import tempfile
import os
import time
import json
import re

# Import functions from conftest.py
from conftest import get_router_ip, connect_to_network, get_current_wifi_connection

def get_mac_address(interface="wlp59s0"):
    """Get the MAC address of the specified network interface."""
    try:
        result = subprocess.run(
            ["ip", "link", "show", interface],
            capture_output=True,
            text=True,
            check=True
        )
        
        # Parse the output to find the MAC address
        # Example output: "2: wlp59s0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc mq state UP mode DORMANT group default qlen 1000
        #     link/ether 12:34:56:78:90:ab brd ff:ff:ff:ff:ff:ff"
        for line in result.stdout.split('\n'):
            if 'link/ether' in line:
                mac_address = line.split()[1]
                return mac_address
                
        raise Exception("Could not find MAC address in ip link output")
    except (subprocess.CalledProcessError, ValueError, IndexError) as e:
        raise Exception(f"Failed to determine MAC address: {e}")

def run_nak_command(command):
    """Helper function to run nak commands."""
    full_command = ["nak"] + command
    result = subprocess.run(full_command, capture_output=True, text=True)
    if result.returncode != 0:
        raise Exception(f"Command failed: {' '.join(full_command)}\nStderr: {result.stderr}\nStdout: {result.stdout}")
    return result.stdout

def run_cdk_cli_command(command, wallet_dir):
    """Helper function to run cdk-cli commands."""
    full_command = ["cdk-cli", "-w", wallet_dir] + command
    result = subprocess.run(full_command, capture_output=True, text=True)
    if result.returncode != 0:
        raise Exception(f"Command failed: {' '.join(full_command)}\nStderr: {result.stderr}\nStdout: {result.stdout}")
    return result.stdout

@pytest.fixture(scope="session")
def ecash_wallet():
    """Create a temporary e-cash wallet funded from the public test mint."""
    wallet_dir = tempfile.mkdtemp()
    print(f"Created temporary e-cash wallet directory: {wallet_dir}")
    
    try:
        # Mint some tokens to the wallet
        mint_result = subprocess.run(
            ["cdk-cli", "-w", wallet_dir, "mint", "https://nofees.testnut.cashu.space", "1000"],
            capture_output=True,
            text=True,
            check=True
        )
        print(f"Minted tokens to wallet: {mint_result.stdout}")
        
        yield wallet_dir
        
    finally:
        # Clean up the temporary directory
        if os.path.exists(wallet_dir):
            import shutil
            shutil.rmtree(wallet_dir)
            print(f"Cleaned up e-cash wallet directory: {wallet_dir}")

@pytest.fixture(scope="session")
def funded_ecash_wallet(ecash_wallet):
    """Ensure the wallet has funds."""
    # Check that we have a balance in the wallet
    balance_output = run_cdk_cli_command(["balance"], ecash_wallet)
    if "sat" not in balance_output:
        raise Exception("Wallet does not have any sats")
    
    return ecash_wallet

def fetch_discovery_event(tollgate_ip):
    """Fetch the discovery event from the TollGate."""
    try:
        result = subprocess.run(
            ["curl", "-s", f"http://{tollgate_ip}:2121/"],
            capture_output=True,
            text=True,
            check=True
        )
        
        # Parse the JSON response
        discovery_event = json.loads(result.stdout)
        return discovery_event
    except subprocess.CalledProcessError as e:
        raise Exception(f"Failed to fetch discovery event: {e}")
    except json.JSONDecodeError as e:
        raise Exception(f"Failed to parse discovery event JSON: {e}")

def generate_customer_identity():
    """Generate a new random customer identity using nak."""
    # Generate a new secret key
    secret_key = run_nak_command(["key", "generate"]).strip()
    
    # Compute the public key from the secret key
    public_key = run_nak_command(["key", "public", secret_key]).strip()
    
    return secret_key, public_key

def create_cashu_token(wallet_dir, amount, mint_url="https://nofees.testnut.cashu.space"):
    """Create a Cashu token for the specified amount."""
    # Send tokens from the wallet
    send_process = subprocess.Popen(
        ["cdk-cli", "-w", wallet_dir, "send", "--mint-url", mint_url],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True
    )
    
    # Send the amount to stdin
    send_output, send_error = send_process.communicate(input=f"{amount}\n")
    
    if send_process.returncode != 0:
        raise Exception(f"Send command failed: {send_error}")
    
    # Extract the token from the send output
    # The token is typically the last line of the output
    token_lines = send_output.strip().split('\n')
    token = None
    for line in reversed(token_lines):
        if line.startswith("cashu"):
            token = line
            break
    
    if token is None:
        raise Exception("Token not found in send output")
    
    return token

def construct_payment_event(customer_secret_key, customer_public_key, tollgate_pubkey, mac_address, cashu_token):
    """Construct a payment event using nak."""
    # Create the event content as a dictionary
    event_content = {
        "kind": 21000,
        "pubkey": customer_public_key,
        "tags": [
            ["p", tollgate_pubkey],
            ["device-identifier", "mac", mac_address],
            ["payment", cashu_token]
        ],
        "content": ""
    }
    
    # Convert to JSON string
    event_json = json.dumps(event_content)
    
    # Use nak to sign the event by passing the JSON via stdin
    sign_result = subprocess.run(
        ["nak", "event", "--sec", customer_secret_key],
        input=event_json,
        capture_output=True,
        text=True,
        check=True
    )
    
    # Parse the signed event
    signed_event = json.loads(sign_result.stdout)
    return signed_event

def send_payment_event(tollgate_ip, payment_event):
    """Send the payment event to the TollGate."""
    # Convert the payment event to JSON
    event_json = json.dumps(payment_event)
    
    # Send the event using curl
    result = subprocess.run(
        ["curl", "-s", "-X", "POST", f"http://{tollgate_ip}:2121/", "-H", "Content-Type: application/json", "-d", event_json],
        capture_output=True,
        text=True
    )
    
    return result

def test_pay_tollgate_and_verify_connectivity(tollgate_networks, funded_ecash_wallet):
    """Test that pays a TollGate and verifies internet connectivity."""
    # Store the previous network connection
    previous_connection = get_current_wifi_connection()
    print(f"Previous network connection: {previous_connection}")
    
    # First, collect all the price information from all TollGate networks
    # We need to do this before connecting to any network so we can generate tokens
    tollgate_info = []
    
    print("Collecting price information from all TollGate networks...")
    for network in tollgate_networks:
        try:
            print(f"Collecting info from TollGate network: {network}")
            
            # Connect to the TollGate network
            connect_to_network(network)
            
            # Wait a bit for the connection to stabilize
            time.sleep(2)
            
            # Get the router IP
            router_ip = get_router_ip()
            if not router_ip:
                print(f"Could not determine router IP for network {network}, skipping...")
                continue
                
            print(f"TollGate IP: {router_ip}")
            
            # Fetch the discovery event from the TollGate
            discovery_event = fetch_discovery_event(router_ip)
            
            # Verify it's a discovery event
            if discovery_event.get("kind") != 10021:
                print(f"Invalid discovery event kind: {discovery_event.get('kind')}")
                continue
                
            # Extract the TollGate's pubkey
            tollgate_pubkey = discovery_event.get("pubkey")
            if not tollgate_pubkey:
                print("TollGate pubkey not found in discovery event")
                continue
                
            print(f"TollGate pubkey: {tollgate_pubkey}")
            
            # Extract price information
            price_per_step = None
            for tag in discovery_event.get("tags", []):
                if tag[0] == "price_per_step" and tag[1] == "cashu" and tag[4] == "https://nofees.testnut.cashu.space":
                    price_per_step = int(tag[2])  # Price
                    break
                    
            if price_per_step is None:
                print("Could not find price_per_step for the testnet mint")
                continue
                
            print(f"Price per step: {price_per_step} sats")
            
            # Store the information for later use
            tollgate_info.append({
                "network": network,
                "router_ip": router_ip,
                "tollgate_pubkey": tollgate_pubkey,
                "price_per_step": price_per_step
            })
            
        except Exception as e:
            print(f"Error collecting info from network {network}: {e}")
            continue
    
    # If we didn't get any TollGate information, fail the test
    if not tollgate_info:
        # Reconnect to the previous network before failing
        if previous_connection:
            try:
                subprocess.run(
                    ["nmcli", "connection", "up", previous_connection],
                    check=True,
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL
                )
                print(f"Reconnected to previous network: {previous_connection}")
            except subprocess.CalledProcessError:
                print(f"Failed to reconnect to previous network: {previous_connection}")
        pytest.fail("Failed to collect information from any TollGate networks")
    
    # Reconnect to the previous network to generate tokens while we have internet access
    if previous_connection:
        try:
            subprocess.run(
                ["nmcli", "connection", "up", previous_connection],
                check=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL
            )
            print(f"Reconnected to previous network: {previous_connection}")
        except subprocess.CalledProcessError:
            print(f"Failed to reconnect to previous network: {previous_connection}")
            pytest.fail("Failed to reconnect to previous network with internet access")
    
    # Wait a bit for the connection to stabilize
    time.sleep(2)
    
    # Generate Cashu tokens for all TollGates while we have internet access
    print("Generating Cashu tokens for all TollGates...")
    cashu_tokens = {}
    
    # Generate tokens for all TollGates
    for info in tollgate_info:
        try:
            token = create_cashu_token(funded_ecash_wallet, info["price_per_step"])
            cashu_tokens[info["network"]] = token
            print(f"Generated Cashu token for network {info['network']}: {token[:20]}...")
        except Exception as e:
            print(f"Error generating token for network {info['network']}: {e}")
            # If we can't generate a token for this network, remove it from our list
            tollgate_info = [i for i in tollgate_info if i["network"] != info["network"]]
    
    # If we didn't generate any tokens, fail the test
    if not cashu_tokens:
        pytest.fail("Failed to generate Cashu tokens for any TollGate networks")
    
    # Now iterate through each TollGate and pay using the pre-generated tokens
    for info in tollgate_info:
        try:
            network = info["network"]
            router_ip = info["router_ip"]
            tollgate_pubkey = info["tollgate_pubkey"]
            price_per_step = info["price_per_step"]
            cashu_token = cashu_tokens[network]
            
            print(f"Testing payment to TollGate network: {network}")
            
            # Connect to the TollGate network
            connect_to_network(network)
            
            # Wait a bit for the connection to stabilize
            time.sleep(2)
            
            # Generate a new customer identity
            customer_secret_key, customer_public_key = generate_customer_identity()
            print(f"Generated customer identity: {customer_public_key}")
            
            # Get the MAC address of the network interface
            mac_address = get_mac_address()
            print(f"MAC address: {mac_address}")
            
            # Construct the payment event
            payment_event = construct_payment_event(
                customer_secret_key,
                customer_public_key,
                tollgate_pubkey,
                mac_address,
                cashu_token
            )
            print(f"Constructed payment event with id: {payment_event.get('id')}")
            
            # Send the payment event to the TollGate
            print("Sending payment event to TollGate...")
            result = send_payment_event(router_ip, payment_event)
            
            # Verify the response
            if result.returncode != 0:
                print(f"Failed to send payment event: {result.stderr}")
                continue
                
            # Check if we got a valid session event in response
            try:
                response_event = json.loads(result.stdout)
                if response_event.get("kind") == 1022:
                    print("Received valid session event from TollGate")
                else:
                    print(f"Unexpected response event kind: {response_event.get('kind')}")
                    print(f"Response: {result.stdout}")
                    continue
            except json.JSONDecodeError:
                print(f"Failed to parse response as JSON: {result.stdout}")
                continue
                
            # Wait for the session to activate
            print("Waiting 5 seconds for session to activate...")
            time.sleep(5)
            
            # Verify internet connectivity
            print("Verifying internet connectivity...")
            ping_result = subprocess.run(
                ["ping", "-c", "1", "-W", "5", "8.8.8.8"],
                capture_output=True,
                text=True
            )
            
            if ping_result.returncode == 0:
                print("SUCCESS: Internet connectivity verified!")
                # If we successfully connected and verified connectivity, we can break
                # since we've proven the concept works
                break
            else:
                print(f"FAILED: Could not ping 8.8.8.8: {ping_result.stderr}")
                
        except Exception as e:
            print(f"Error testing network {network}: {e}")
            continue
            
    # If we get here without breaking, it means we didn't successfully verify connectivity
    # This will cause the test to fail
    else:
        pytest.fail("Failed to successfully pay and verify connectivity with any TollGate")