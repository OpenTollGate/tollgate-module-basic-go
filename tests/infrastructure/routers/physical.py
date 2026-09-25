"""
Physical router implementation using SSH.

Manages real OpenWRT routers via SSH connections.
"""

import asyncio
import subprocess
from typing import Dict, Any, Optional
import logging

from .base import RouterInterface, ConnectionError, ExecutionError, FlashError

logger = logging.getLogger(__name__)


class PhysicalRouter(RouterInterface):
    """Physical router accessed via SSH."""

    def __init__(self, name: str, config: Dict[str, Any]):
        """
        Initialize physical router.

        Args:
            name: Router identifier
            config: Configuration with keys:
                - host: IP address or hostname
                - password: SSH password
                - username: SSH username (default: root)
                - port: SSH port (default: 22)
                - arch: Router architecture
                - interface: Network interface name
        """
        super().__init__(name, config)
        self.host = config.get('host')
        self.password = config.get('password')
        self.username = config.get('username', 'root')
        self.port = config.get('port', 22)
        self.arch = config.get('arch', 'unknown')
        self.interface = config.get('interface', 'wlan0')

        if not self.host or not self.password:
            raise ValueError(f"Physical router {name} missing host or password in config")

        self._ssh_process = None

    async def connect(self, timeout: int = 60) -> bool:
        """
        Establish SSH connection to the router.

        Uses sshpass for password authentication (same as existing tests).
        """
        logger.info(f"Connecting to physical router {self.name} at {self.host}")

        try:
            # Test SSH connection using sshpass
            cmd = [
                "sshpass", "-p", self.password,
                "ssh", "-o", "StrictHostKeyChecking=no",
                "-o", "ConnectTimeout={timeout}",
                "-o", "UserKnownHostsFile=/dev/null",
                f"{self.username}@{self.host}",
                "echo connected"
            ]

            process = await asyncio.create_subprocess_exec(
                *cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )

            try:
                stdout, stderr = await asyncio.wait_for(
                    process.communicate(),
                    timeout=timeout
                )

                if process.returncode == 0 and b'connected' in stdout:
                    self._connected = True
                    logger.info(f"Successfully connected to {self.name}")
                    return True
                else:
                    logger.error(f"Failed to connect to {self.name}: {stderr.decode()}")
                    return False

            except asyncio.TimeoutError:
                logger.error(f"Connection timeout to {self.name} after {timeout}s")
                return False

        except Exception as e:
            logger.error(f"Error connecting to {self.name}: {e}")
            raise ConnectionError(f"Connection failed: {e}", self.name) from e

    async def disconnect(self) -> bool:
        """Disconnect from router (no-op for SSH stateless connections)."""
        logger.info(f"Disconnecting from {self.name}")
        self._connected = False
        return True

    async def execute_command(self, command: str, timeout: int = 30) -> str:
        """
        Execute command on router via SSH.

        Returns command output.
        """
        if not self._connected:
            raise ConnectionError("Not connected to router", self.name)

        logger.debug(f"Executing command on {self.name}: {command}")

        ssh_cmd = [
            "sshpass", "-p", self.password,
            "ssh", "-o", "StrictHostKeyChecking=no",
            "-o", "ConnectTimeout=10",
            "-o", "UserKnownHostsFile=/dev/null",
            f"{self.username}@{self.host}",
            command
        ]

        try:
            process = await asyncio.create_subprocess_exec(
                *ssh_cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )

            stdout, stderr = await asyncio.wait_for(
                process.communicate(),
                timeout=timeout
            )

            if process.returncode != 0:
                error_msg = stderr.decode().strip()
                logger.error(f"Command failed on {self.name}: {error_msg}")
                raise ExecutionError(f"Command failed: {error_msg}", self.name)

            output = stdout.decode().strip()
            logger.debug(f"Command output: {output[:100]}...")
            return output

        except asyncio.TimeoutError:
            logger.error(f"Command timeout on {self.name} after {timeout}s")
            raise ExecutionError(f"Command timeout after {timeout}s", self.name)
        except Exception as e:
            logger.error(f"Error executing command on {self.name}: {e}")
            raise ExecutionError(f"Execution error: {e}", self.name) from e

    async def upload_file(self, local_path: str, remote_path: str) -> bool:
        """
        Upload file to router using ssh and cat.

        This is the same method used in existing tests.
        """
        logger.info(f"Uploading {local_path} to {self.name}:{remote_path}")

        try:
            ssh_cmd = [
                "sshpass", "-p", self.password,
                "ssh", "-o", "StrictHostKeyChecking=no",
                "-o", "ConnectTimeout=10",
                "-o", "UserKnownHostsFile=/dev/null",
                f"{self.username}@{self.host}",
                f"cat > {remote_path}"
            ]

            with open(local_path, 'rb') as f:
                process = await asyncio.create_subprocess_exec(
                    *ssh_cmd,
                    stdin=f,
                    stdout=asyncio.subprocess.PIPE,
                    stderr=asyncio.subprocess.PIPE
                )

                stdout, stderr = await process.communicate()

                if process.returncode == 0:
                    logger.info(f"Successfully uploaded file to {self.name}")
                    return True
                else:
                    logger.error(f"Upload failed to {self.name}: {stderr.decode()}")
                    return False

        except Exception as e:
            logger.error(f"Error uploading file to {self.name}: {e}")
            return False

    async def download_file(self, remote_path: str, local_path: str) -> bool:
        """
        Download file from router using SSH.
        """
        logger.info(f"Downloading {self.name}:{remote_path} to {local_path}")

        try:
            ssh_cmd = [
                "sshpass", "-p", self.password,
                "ssh", "-o", "StrictHostKeyChecking=no",
                "-o", "ConnectTimeout=10",
                "-o", "UserKnownHostsFile=/dev/null",
                f"{self.username}@{self.host}",
                f"cat {remote_path}"
            ]

            process = await asyncio.create_subprocess_exec(
                *ssh_cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )

            stdout, stderr = await process.communicate()

            if process.returncode == 0:
                with open(local_path, 'wb') as f:
                    f.write(stdout)
                logger.info(f"Successfully downloaded file from {self.name}")
                return True
            else:
                logger.error(f"Download failed from {self.name}: {stderr.decode()}")
                return False

        except Exception as e:
            logger.error(f"Error downloading file from {self.name}: {e}")
            return False

    async def install_firmware(self, firmware_path: str, timeout: int = 300) -> bool:
        """
        Flash firmware to router.

        Uploads firmware and executes sysupgrade command.
        """
        logger.info(f"Flashing firmware on {self.name}: {firmware_path}")

        try:
            # Upload firmware to /tmp
            remote_path = f"/tmp/{firmware_path.split('/')[-1]}"
            upload_success = await self.upload_file(firmware_path, remote_path)

            if not upload_success:
                raise FlashError("Failed to upload firmware", self.name)

            # Execute sysupgrade (router will reboot)
            flash_cmd = f"sysupgrade -n {remote_path}"
            logger.info(f"Executing flash command on {self.name}")

            try:
                # Don't wait for completion - router will reboot
                process = await asyncio.create_subprocess_exec(
                    "sshpass", "-p", self.password,
                    "ssh", "-o", "StrictHostKeyChecking=no",
                    "-o", "ConnectTimeout=10",
                    "-o", "UserKnownHostsFile=/dev/null",
                    f"{self.username}@{self.host}",
                    flash_cmd
                )

                # Wait a short time to verify command started
                await asyncio.wait_for(
                    process.communicate(),
                    timeout=10
                )

                logger.info(f"Firmware flash initiated on {self.name}")
                self._connected = False
                return True

            except asyncio.TimeoutError:
                # Expected - router is rebooting
                logger.info(f"Router {self.name} is rebooting (expected)")
                self._connected = False
                return True

        except Exception as e:
            logger.error(f"Error flashing firmware on {self.name}: {e}")
            raise FlashError(f"Firmware flash failed: {e}", self.name) from e

    async def install_package(self, package_path: str, timeout: int = 120) -> bool:
        """
        Install .ipk package on router.

        Uploads package and uses opkg install.
        """
        logger.info(f"Installing package on {self.name}: {package_path}")

        try:
            # Upload package to /tmp
            remote_path = f"/tmp/{package_path.split('/')[-1]}"
            upload_success = await self.upload_file(package_path, remote_path)

            if not upload_success:
                raise ExecutionError("Failed to upload package", self.name)

            # Install package using opkg
            install_cmd = f"cd /tmp && opkg install {remote_path.split('/')[-1]}"
            await self.execute_command(install_cmd, timeout=timeout)

            logger.info(f"Successfully installed package on {self.name}")
            return True

        except Exception as e:
            logger.error(f"Error installing package on {self.name}: {e}")
            return False

    async def get_info(self) -> Dict[str, Any]:
        """
        Get router information.
        """
        try:
            # Get architecture
            arch_cmd = "uname -m"
            arch = await self.execute_command(arch_cmd)

            # Get OpenWRT version
            version_cmd = "cat /etc/openwrt_release"
            version_output = await self.execute_command(version_cmd)
            version = version_output.strip()

            # Get uptime
            uptime_cmd = "cat /proc/uptime"
            uptime_output = await self.execute_command(uptime_cmd)
            uptime_seconds = uptime_output.split()[0]

            # Get system load
            load_cmd = "cat /proc/loadavg"
            load_output = await self.execute_command(load_cmd)
            load_avg = load_output.split()[:3]

            return {
                'arch': arch,
                'version': version,
                'ip': self.host,
                'uptime': float(uptime_seconds),
                'load': [float(x) for x in load_avg],
                'type': 'physical'
            }

        except Exception as e:
            logger.error(f"Error getting info from {self.name}: {e}")
            raise ExecutionError(f"Failed to get info: {e}", self.name) from e

    async def reboot(self, timeout: int = 180) -> bool:
        """
        Reboot the router.
        """
        logger.info(f"Rebooting router {self.name}")

        try:
            try:
                # Execute reboot command
                await self.execute_command("reboot", timeout=10)
            except ExecutionError:
                # Command may fail due to connection being dropped
                pass

            self._connected = False
            logger.info(f"Reboot initiated on {self.name}")
            return True

        except Exception as e:
            logger.error(f"Error rebooting {self.name}: {e}")
            return False

    async def is_online(self) -> bool:
        """
        Check if router is online using ping.
        """
        try:
            cmd = ["ping", "-c", "1", "-W", "5", self.host]
            process = await asyncio.create_subprocess_exec(
                *cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )

            stdout, stderr = await asyncio.wait_for(
                process.communicate(),
                timeout=10
            )

            online = process.returncode == 0
            if online:
                logger.debug(f"Router {self.name} is online")
            else:
                logger.debug(f"Router {self.name} is offline")
            return online

        except Exception as e:
            logger.debug(f"Error checking online status for {self.name}: {e}")
            return False

    async def get_logs(self, lines: int = 100) -> str:
        """
        Retrieve logs from router using logread.
        """
        try:
            cmd = f"logread -l {lines}"
            logs = await self.execute_command(cmd)
            return logs
        except Exception as e:
            logger.error(f"Error getting logs from {self.name}: {e}")
            return ""

    async def cleanup(self) -> bool:
        """
        Clean up temporary files on router.
        """
        try:
            cleanup_cmd = "rm -f /tmp/*.ipk /tmp/*.bin"
            await self.execute_command(cleanup_cmd)
            logger.info(f"Cleanup completed on {self.name}")
            return True
        except Exception as e:
            logger.error(f"Error during cleanup on {self.name}: {e}")
            return False
