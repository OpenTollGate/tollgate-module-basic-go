import pytest
import subprocess
import tempfile
import shutil
import os

LOCAL_MINT_URL = "https://nofees.testnut.cashu.space"

@pytest.fixture(scope="session")
def ecash_wallet():
    """Create a temporary e-cash wallet funded from the public test mint."""
    wallet_dir = tempfile.mkdtemp()
    print(f"Created temporary e-cash wallet directory: {wallet_dir}")

    try:
        # Mint some tokens to the wallet
        mint_result = subprocess.run(
            ["cdk-cli", "-w", wallet_dir, "mint", LOCAL_MINT_URL, "1000"],
            capture_output=True,
            text=True,
            check=True
        )
        print(f"Minted tokens to wallet: {mint_result.stdout}")

        yield wallet_dir

    finally:
        # Clean up the temporary directory
        if os.path.exists(wallet_dir):
            shutil.rmtree(wallet_dir)
            print(f"Cleaned up e-cash wallet directory: {wallet_dir}")

@pytest.fixture(scope="session")
def test_token(ecash_wallet):
    """Generate a test token."""
    try:
        # Send tokens from the wallet
        send_process = subprocess.Popen(
            ["cdk-cli", "-w", ecash_wallet, "send", "--mint-url", LOCAL_MINT_URL],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True
        )

        # Send the amount to stdin
        send_output, send_error = send_process.communicate(input="100\n")

        if send_process.returncode != 0:
            raise Exception(f"Send command failed: {send_error}")

        # Extract the token from the send output
        token_lines = send_output.strip().split('\n')
        token = None
        for line in reversed(token_lines):
            if line.startswith("cashu"):
                token = line
                break
        
        if token is None:
            raise Exception("Token not found in send output")

        return token
            
    except Exception as e:
        raise Exception(f"Failed to generate test token: {e}")
