import pytest
import subprocess
import json
import os
import tempfile

def run_cdk_cli_command(command, wallet_dir):
    """Helper function to run cdk-cli commands."""
    full_command = ["cdk-cli", "-w", wallet_dir] + command
    result = subprocess.run(full_command, capture_output=True, text=True)
    if result.returncode != 0:
        raise Exception(f"Command failed: {' '.join(full_command)}\nStderr: {result.stderr}\nStdout: {result.stdout}")
    return result.stdout

def test_mint_info(ecash_wallet):
    """Test that we can get mint info from the public mint."""
    # Get mint info
    output = run_cdk_cli_command(["mint-info", "https://nofees.testnut.cashu.space"], ecash_wallet)
    
    # Verify that we got the expected mint information
    # Note: The exact values might change, so we're checking for the presence of keys
    assert "name" in output
    assert "version" in output
    assert "description" in output
    assert "nuts" in output

def test_token_generation_and_reception(ecash_wallet):
    """Test that we can generate and receive tokens."""
    # Create a second wallet for receiving tokens
    with tempfile.TemporaryDirectory() as wallet2_dir:
        # Check that we have a balance in the main wallet
        balance_output = run_cdk_cli_command(["balance"], ecash_wallet)
        assert "sat" in balance_output  # We should have some sats
        
        # Send tokens from the main wallet
        send_process = subprocess.Popen(
            ["cdk-cli", "-w", ecash_wallet, "send", "--mint-url", "https://nofees.testnut.cashu.space"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True
        )
        
        # Send the amount to stdin
        send_output, send_error = send_process.communicate(input="500\n")
        
        if send_process.returncode != 0:
            raise Exception(f"Send command failed: {send_error}")
        
        # Extract the token from the send output
        # The token is the last line of the output
        token_lines = send_output.strip().split('\n')
        token = None
        for line in reversed(token_lines):
            if line.startswith("cashu"):
                token = line
                break
        
        # Verify that the token is not empty
        assert token is not None, "Token not found in send output"
        assert token.startswith("cashu"), f"Invalid token format: {token}"
        
        # Receive the token in wallet2
        receive_output = run_cdk_cli_command(["receive", token], wallet2_dir)
        
        # Verify that we received the tokens
        assert "Received:" in receive_output
        
        # Check the balance in wallet2
        balance_output2 = run_cdk_cli_command(["balance"], wallet2_dir)
        assert "sat" in balance_output2  # We should have some balance now

def test_token_transfer_between_wallets(ecash_wallet):
    """Test that we can transfer tokens between wallets and verify balance changes."""
    # Create a second wallet for receiving tokens
    with tempfile.TemporaryDirectory() as wallet2_dir:
        # Check initial balances
        initial_balance_output1 = run_cdk_cli_command(["balance"], ecash_wallet)
        initial_balance_output2 = run_cdk_cli_command(["balance"], wallet2_dir)
        
        # Parse initial balances (assuming format "X sat")
        def parse_balance(output):
            lines = output.strip().split('\n')
            for line in lines:
                if 'sat' in line:
                    return int(line.split()[0])
            return 0
        
        initial_balance1 = parse_balance(initial_balance_output1)
        initial_balance2 = parse_balance(initial_balance_output2)
        
        # Send tokens from the main wallet
        send_process = subprocess.Popen(
            ["cdk-cli", "-w", ecash_wallet, "send", "--mint-url", "https://nofees.testnut.cashu.space"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True
        )
        
        # Send the amount to stdin
        send_output, send_error = send_process.communicate(input="500\n")
        
        if send_process.returncode != 0:
            raise Exception(f"Send command failed: {send_error}")
        
        # Extract the token from the send output
        # The token is the last line of the output
        token_lines = send_output.strip().split('\n')
        token = None
        for line in reversed(token_lines):
            if line.startswith("cashu"):
                token = line
                break
        
        # Verify that the token is not empty
        assert token is not None, "Token not found in send output"
        assert token.startswith("cashu"), f"Invalid token format: {token}"
        
        # Check sender's balance after sending
        post_send_balance_output1 = run_cdk_cli_command(["balance"], ecash_wallet)
        post_send_balance1 = parse_balance(post_send_balance_output1)
        
        # Verify that sender's balance decreased (allowing for fees)
        # We sent 500 sat, so balance should be at least 500 less than initial
        assert post_send_balance1 <= initial_balance1 - 500, f"Sender's balance did not decrease properly. Initial: {initial_balance1}, Post-send: {post_send_balance1}"
        
        # Receive the token in wallet2
        receive_output = run_cdk_cli_command(["receive", token], wallet2_dir)
        
        # Verify that we received the tokens
        assert "Received:" in receive_output
        
        # Check receiver's balance after receiving
        post_receive_balance_output2 = run_cdk_cli_command(["balance"], wallet2_dir)
        post_receive_balance2 = parse_balance(post_receive_balance_output2)
        
        # Verify that receiver's balance increased by 500 sat
        assert post_receive_balance2 == initial_balance2 + 500, f"Receiver's balance did not increase properly. Initial: {initial_balance2}, Post-receive: {post_receive_balance2}"