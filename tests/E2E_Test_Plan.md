# E2E Test Plan for tollgate-module-basic-go

This document outlines the plan for setting up an end-to-end testing environment for the `tollgate-module-basic-go` project using `pytest`. The testing will focus on validating the functionality of the `tollgate-cli` with `testnut` e-cash.

## 1. Ansible Playbook

The following Ansible playbook, `tests/setup_cdk_testing.yml`, will automate the setup of the testing environment.

```yaml
---
- name: Setup CDK Testing Environment
  hosts: localhost
  become: yes
  vars:
    cdk_repo_url: "https://github.com/cashubtc/cdk.git"
    user_home: "{{ lookup('env', 'HOME') }}"
    cdk_repo_path: "{{ user_home }}/cdk"
    project_path: "{{ user_home }}/tollgate-module-basic-go"
    venv_path: "{{ project_path }}/venv"
  tasks:
    - name: Debug user home and paths
      debug:
        msg: "User home: {{ user_home }}, CDK repo path: {{ cdk_repo_path }}, Project path: {{ project_path }}"

    - name: Ensure Python 3 and pip are installed
      apt:
        name:
          - python3
          - python3-pip
          - python3-venv
        state: present

    - name: Create virtual environment
      command: python3 -m venv {{ venv_path }}
      args:
        creates: "{{ venv_path }}/bin/activate"

    - name: Install pytest in the virtual environment
      pip:
        name: pytest
        virtualenv: "{{ venv_path }}"
        virtualenv_command: python3 -m venv

    - name: Check if Rust is installed
      command: rustc --version
      register: rust_version
      ignore_errors: yes
      become: no
      become_user: "{{ lookup('env', 'USER') }}"

    - name: Install Rust and Cargo if not present
      shell: |
        curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
        source "$HOME/.cargo/env"
      args:
        executable: /bin/bash
      environment:
        HOME: "{{ user_home }}"
      when: rust_version.rc != 0
      become: no
      become_user: "{{ lookup('env', 'USER') }}"

    - name: Check if cdk repository exists
      stat:
        path: "{{ cdk_repo_path }}"
      register: cdk_repo_stat
      become: no
      become_user: "{{ lookup('env', 'USER') }}"

    - name: Clone cdk repository if it doesn't exist
      git:
        repo: "{{ cdk_repo_url }}"
        dest: "{{ cdk_repo_path }}"
        clone: yes
        update: no
      when: not cdk_repo_stat.stat.exists
      become: no
      become_user: "{{ lookup('env', 'USER') }}"

    - name: Compile cdk-cli in release mode
      shell: |
        source "$HOME/.cargo/env"
        cargo build --release --bin cdk-cli
      args:
        chdir: "{{ cdk_repo_path }}"
      environment:
        HOME: "{{ user_home }}"
      become: no
      become_user: "{{ lookup('env', 'USER') }}"

    - name: Install cdk-cli
      copy:
        src: "{{ cdk_repo_path }}/target/release/cdk-cli"
        dest: "/usr/local/bin/cdk-cli"
        mode: "0755"
        remote_src: yes
```

## 2. Pytest Fixtures (`conftest.py`)

The file `tests/conftest.py` will contain the fixtures for our tests.

```python
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
        token_lines = send_output.strip().split('\\n')
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

```

## 3. Pytest Tests (`test_tollgate_cli.py`)

The initial test file, `tests/test_tollgate_cli.py`, will validate the `tollgate-cli` functionality.

```python
import subprocess

def run_tollgate_cli_command(command):
    """Helper function to run tollgate-cli commands."""
    full_command = ["go", "run", "src/cli/server.go"] + command
    result = subprocess.run(full_command, capture_output=True, text=True)
    if result.returncode != 0:
        raise Exception(f"Command failed: {' '.join(full_command)}\nStderr: {result.stderr}\nStdout: {result.stdout}")
    return result.stdout

def test_receive_ecash(test_token):
    """Test that the tollgate-cli can receive e-cash."""
    # Receive the token
    output = run_tollgate_cli_command(["receive", "--token", test_token])
    
    # Verify that we received the tokens
    assert "Received" in output
```

## 4. README

Finally, `tests/README.md` will document the setup and execution of these tests.

```markdown
# End-to-End Tests for tollgate-module-basic-go

This directory contains the end-to-end tests for the `tollgate-module-basic-go` project. These tests use `pytest` and `ansible` to automate the testing process.

## Setup

1.  **Install Ansible:**
    
    If you don't have Ansible installed, you can install it with pip:
    
    ```bash
    pip install ansible
    ```
    
2.  **Run the Ansible Playbook:**
    
    The Ansible playbook will set up the entire testing environment, including Python, Rust, and the `cdk-cli`.
    
    ```bash
    ansible-playbook tests/setup_cdk_testing.yml
    ```
    

## Running the Tests

To run the tests, use `pytest`:

```bash
/bin/python3 -m pytest tests/
```