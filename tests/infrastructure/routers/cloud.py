"""
Cloud router implementation for providers like Hetzner.

Supports creating, configuring, and managing cloud-based OpenWRT instances.
"""

import asyncio
from typing import Dict, Any, Optional, List
import logging
from datetime import datetime, timedelta

from .base import RouterInterface, ConnectionError, ExecutionError

logger = logging.getLogger(__name__)


class CloudRouter(RouterInterface):
    """Cloud router managed via provider API."""

    def __init__(self, name: str, config: Dict[str, Any]):
        """
        Initialize cloud router.

        Args:
            name: Router identifier
            config: Configuration with keys:
                - provider: Provider name (hetzner, digitalocean, etc.)
                - api_token: Provider API token
                - server_id: Existing server ID (optional)
                - server_type: Server type to create
                - image: OS image (OpenWRT)
                - region: Region (optional)
        """
        super().__init__(name, config)
        self.provider = config.get('provider', 'hetzner')
        self.api_token = config.get('api_token')
        self.server_id = config.get('server_id')
        self.server_type = config.get('server_type', 'cx21')
        self.image = config.get('image', 'openwrt-23.05')
        self.region = config.get('region', 'fsn1')

        if not self.api_token:
            raise ValueError(f"Cloud router {name} missing api_token")

        self._server_info = None
        self._server_ip = None

    async def connect(self, timeout: int = 60) -> bool:
        """
        Create cloud server and establish connection.

        For Hetzner:
        1. Create server if server_id not provided
        2. Wait for server to be ready
        3. SSH into the server
        """
        logger.info(f"Connecting to cloud router {self.name} (provider: {self.provider})")

        try:
            if not self.server_id:
                # Create new server
                server_id = await self._create_server()
                self.server_id = server_id
                logger.info(f"Created server {server_id} for {self.name}")

            # Wait for server to be ready
            await self._wait_for_server_ready(timeout=timeout)

            # Get server IP
            self._server_ip = await self._get_server_ip()
            if not self._server_ip:
                raise ConnectionError("Could not determine server IP", self.name)

            logger.info(f"Server {self.server_id} ready at {self._server_ip}")

            # Establish SSH connection
            # For now, we'll reuse physical router SSH logic
            # In a full implementation, we'd use asyncssh or paramiko
            # This is a placeholder for the connection logic

            self._connected = True
            return True

        except Exception as e:
            logger.error(f"Error connecting to cloud router {self.name}: {e}")
            raise ConnectionError(f"Connection failed: {e}", self.name) from e

    async def _create_server(self) -> str:
        """
        Create cloud server using provider API.

        Returns server ID.
        """
        # Placeholder for provider API implementation
        # For Hetzner, would use hcloud-python-client
        # For DigitalOcean, would use python-digitalocean
        # This is a simplified version that logs what would happen

        logger.info(f"Creating cloud server (type: {self.server_type}, image: {self.image}, region: {self.region})")

        # Simulate API call delay
        await asyncio.sleep(5)

        # Return mock server ID
        return f"srv-{datetime.now().strftime('%Y%m%d%H%M%S')}"

    async def _wait_for_server_ready(self, timeout: int = 300) -> None:
        """
        Wait for server to be ready and accept SSH connections.
        """
        logger.info(f"Waiting for server {self.server_id} to be ready...")

        start_time = datetime.now()
        ready = False

        while datetime.now() - start_time < timedelta(seconds=timeout):
            # Check if server responds to ping
            ping_cmd = ["ping", "-c", "1", "-W", "5", self._server_ip]
            process = await asyncio.create_subprocess_exec(
                *ping_cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )

            await process.wait()

            if process.returncode == 0:
                ready = True
                break

            logger.debug(f"Server {self.server_id} not ready yet, waiting...")
            await asyncio.sleep(10)

        if not ready:
            raise ConnectionError(f"Server {self.server_id} not ready after {timeout}s", self.name)

        logger.info(f"Server {self.server_id} is ready")

    async def _get_server_ip(self) -> Optional[str]:
        """
        Get server IP address from provider API.
        """
        # Placeholder for provider API implementation
        # Would query provider API for server details

        if self._server_id.startswith('srv-'):
            # Return mock IP for testing
            return f"10.0.{hash(self.server_id) % 256}.{(hash(self.server_id) >> 8) % 256}"

        return None

    async def disconnect(self) -> bool:
        """
        Disconnect from cloud server.

        Doesn't delete the server - just disconnects SSH.
        """
        logger.info(f"Disconnecting from cloud router {self.name}")
        self._connected = False
        return True

    async def execute_command(self, command: str, timeout: int = 30) -> str:
        """
        Execute command on cloud server via SSH.

        For now, this delegates to physical router logic.
        """
        if not self._connected or not self._server_ip:
            raise ConnectionError("Not connected to cloud server", self.name)

        # Import here to avoid circular dependency
        from .physical import PhysicalRouter

        # Create temporary physical router instance for SSH operations
        temp_config = {
            'host': self._server_ip,
            'password': self.config.get('password', 'root'),
            'username': 'root',
            'port': 22
        }

        temp_router = PhysicalRouter(f"{self.name}-ssh", temp_config)
        await temp_router.connect()

        try:
            return await temp_router.execute_command(command, timeout)
        finally:
            await temp_router.disconnect()

    async def upload_file(self, local_path: str, remote_path: str) -> bool:
        """
        Upload file to cloud server.
        """
        if not self._connected or not self._server_ip:
            raise ConnectionError("Not connected to cloud server", self.name)

        from .physical import PhysicalRouter

        temp_config = {
            'host': self._server_ip,
            'password': self.config.get('password', 'root'),
            'username': 'root',
            'port': 22
        }

        temp_router = PhysicalRouter(f"{self.name}-ssh", temp_config)
        await temp_router.connect()

        try:
            return await temp_router.upload_file(local_path, remote_path)
        finally:
            await temp_router.disconnect()

    async def download_file(self, remote_path: str, local_path: str) -> bool:
        """
        Download file from cloud server.
        """
        if not self._connected or not self._server_ip:
            raise ConnectionError("Not connected to cloud server", self.name)

        from .physical import PhysicalRouter

        temp_config = {
            'host': self._server_ip,
            'password': self.config.get('password', 'root'),
            'username': 'root',
            'port': 22
        }

        temp_router = PhysicalRouter(f"{self.name}-ssh", temp_config)
        await temp_router.connect()

        try:
            return await temp_router.download_file(remote_path, local_path)
        finally:
            await temp_router.disconnect()

    async def install_firmware(self, firmware_path: str, timeout: int = 300) -> bool:
        """
        Flash firmware on cloud server.

        Note: Most cloud providers don't support firmware flashing.
        This would typically require reinstalling the server with a new image.
        """
        logger.warning(f"Firmware flashing not supported on cloud router {self.name}")
        logger.info(f"Use provider API to recreate server with new image instead")

        # For now, we'll return False
        return False

    async def install_package(self, package_path: str, timeout: int = 120) -> bool:
        """
        Install package on cloud server.
        """
        if not self._connected or not self._server_ip:
            raise ConnectionError("Not connected to cloud server", self.name)

        from .physical import PhysicalRouter

        temp_config = {
            'host': self._server_ip,
            'password': self.config.get('password', 'root'),
            'username': 'root',
            'port': 22
        }

        temp_router = PhysicalRouter(f"{self.name}-ssh", temp_config)
        await temp_router.connect()

        try:
            return await temp_router.install_package(package_path, timeout)
        finally:
            await temp_router.disconnect()

    async def get_info(self) -> Dict[str, Any]:
        """
        Get cloud server information.
        """
        if not self._server_id:
            raise ConnectionError("Server not created", self.name)

        try:
            # Execute commands to get info
            from .physical import PhysicalRouter

            temp_config = {
                'host': self._server_ip,
                'password': self.config.get('password', 'root'),
                'username': 'root',
                'port': 22
            }

            temp_router = PhysicalRouter(f"{self.name}-ssh", temp_config)
            await temp_router.connect()

            try:
                info = await temp_router.get_info()
                info['server_id'] = self.server_id
                info['server_type'] = self.server_type
                info['image'] = self.image
                info['region'] = self.region
                return info
            finally:
                await temp_router.disconnect()

        except Exception as e:
            logger.error(f"Error getting info from {self.name}: {e}")
            raise ExecutionError(f"Failed to get info: {e}", self.name) from e

    async def reboot(self, timeout: int = 180) -> bool:
        """
        Reboot cloud server.
        """
        if not self._connected or not self._server_id:
            raise ConnectionError("Not connected to cloud server", self.name)

        logger.info(f"Rebooting cloud server {self.name}")

        # For cloud servers, we'd typically use provider API to reboot
        logger.info(f"Use provider API to reboot server {self.server_id}")

        # Placeholder - would call provider API
        await asyncio.sleep(5)

        self._connected = False
        return True

    async def is_online(self) -> bool:
        """
        Check if cloud server is online.
        """
        if not self._server_ip:
            return False

        try:
            ping_cmd = ["ping", "-c", "1", "-W", "5", self._server_ip]
            process = await asyncio.create_subprocess_exec(
                *ping_cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )

            await process.wait()

            online = process.returncode == 0
            if online:
                logger.debug(f"Cloud server {self.name} is online")
            else:
                logger.debug(f"Cloud server {self.name} is offline")
            return online

        except Exception as e:
            logger.debug(f"Error checking online status for {self.name}: {e}")
            return False

    async def get_logs(self, lines: int = 100) -> str:
        """
        Retrieve logs from cloud server.
        """
        if not self._connected or not self._server_ip:
            return ""

        from .physical import PhysicalRouter

        temp_config = {
            'host': self._server_ip,
            'password': self.config.get('password', 'root'),
            'username': 'root',
            'port': 22
        }

        temp_router = PhysicalRouter(f"{self.name}-ssh", temp_config)
        await temp_router.connect()

        try:
            return await temp_router.get_logs(lines)
        finally:
            await temp_router.disconnect()

    async def cleanup(self) -> bool:
        """
        Clean up temporary files on cloud server.
        """
        if not self._connected or not self._server_ip:
            return False

        from .physical import PhysicalRouter

        temp_config = {
            'host': self._server_ip,
            'password': self.config.get('password', 'root'),
            'username': 'root',
            'port': 22
        }

        temp_router = PhysicalRouter(f"{self.name}-ssh", temp_config)
        await temp_router.connect()

        try:
            return await temp_router.cleanup()
        finally:
            await temp_router.disconnect()

    async def delete(self) -> bool:
        """
        Delete the cloud server (cleanup).

        This is additional method specific to cloud routers.
        """
        if not self.server_id:
            logger.warning(f"No server to delete for {self.name}")
            return False

        logger.info(f"Deleting cloud server {self.server_id} ({self.name})")

        # Placeholder for provider API call
        logger.info(f"Use provider API to delete server {self.server_id}")
        await asyncio.sleep(2)

        self._connected = False
        self._server_id = None
        self._server_ip = None

        return True
