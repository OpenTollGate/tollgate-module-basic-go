"""
Router abstraction layer for TollGate testing infrastructure.

Provides a unified interface for interacting with different router types:
- Physical routers (SSH access)
- Cloud routers (Hetzner API)
- Docker routers (simulation)
"""

from abc import ABC, abstractmethod
from typing import Optional, Dict, Any
import logging

logger = logging.getLogger(__name__)


class RouterInterface(ABC):
    """Base interface for all router types."""

    def __init__(self, name: str, config: Dict[str, Any]):
        """
        Initialize router instance.

        Args:
            name: Router identifier
            config: Router-specific configuration
        """
        self.name = name
        self.config = config
        self.tags = config.get('tags', [])
        self._connected = False

    @abstractmethod
    async def connect(self, timeout: int = 60) -> bool:
        """
        Establish connection to the router.

        Args:
            timeout: Connection timeout in seconds

        Returns:
            True if connected successfully, False otherwise
        """
        pass

    @abstractmethod
    async def disconnect(self) -> bool:
        """
        Disconnect from the router.

        Returns:
            True if disconnected successfully, False otherwise
        """
        pass

    @abstractmethod
    async def execute_command(self, command: str, timeout: int = 30) -> str:
        """
        Execute a command on the router.

        Args:
            command: Command to execute
            timeout: Command timeout in seconds

        Returns:
            Command output

        Raises:
            RouterError: If command execution fails
        """
        pass

    @abstractmethod
    async def upload_file(self, local_path: str, remote_path: str) -> bool:
        """
        Upload a file to the router.

        Args:
            local_path: Local file path
            remote_path: Remote file path

        Returns:
            True if upload successful, False otherwise
        """
        pass

    @abstractmethod
    async def download_file(self, remote_path: str, local_path: str) -> bool:
        """
        Download a file from the router.

        Args:
            remote_path: Remote file path
            local_path: Local file path

        Returns:
            True if download successful, False otherwise
        """
        pass

    @abstractmethod
    async def install_firmware(self, firmware_path: str, timeout: int = 300) -> bool:
        """
        Flash firmware to the router.

        Args:
            firmware_path: Path to firmware image
            timeout: Flash timeout in seconds

        Returns:
            True if flash successful, False otherwise
        """
        pass

    @abstractmethod
    async def install_package(self, package_path: str, timeout: int = 120) -> bool:
        """
        Install a package on the router.

        Args:
            package_path: Path to .ipk package
            timeout: Installation timeout in seconds

        Returns:
            True if installation successful, False otherwise
        """
        pass

    @abstractmethod
    async def get_info(self) -> Dict[str, Any]:
        """
        Get router information.

        Returns:
            Dictionary with router information:
            - arch: Architecture (mips, arm, etc.)
            - version: OpenWRT version
            - ip: IP address
            - uptime: System uptime
            - load: System load
        """
        pass

    @abstractmethod
    async def reboot(self, timeout: int = 180) -> bool:
        """
        Reboot the router.

        Args:
            timeout: Wait timeout in seconds before considering reboot failed

        Returns:
            True if reboot initiated successfully, False otherwise
        """
        pass

    @abstractmethod
    async def is_online(self) -> bool:
        """
        Check if router is online and accessible.

        Returns:
            True if online, False otherwise
        """
        pass

    @abstractmethod
    async def get_logs(self, lines: int = 100) -> str:
        """
        Retrieve logs from the router.

        Args:
            lines: Number of log lines to retrieve

        Returns:
            Log output as string
        """
        pass

    @abstractmethod
    async def cleanup(self) -> bool:
        """
        Clean up resources (temporary files, etc.).

        Returns:
            True if cleanup successful, False otherwise
        """
        pass

    @property
    def is_connected(self) -> bool:
        """Check if router is currently connected."""
        return self._connected

    @property
    def type(self) -> str:
        """Return router type identifier."""
        return self.config.get('type', 'unknown')


class RouterError(Exception):
    """Exception raised for router-related errors."""

    def __init__(self, message: str, router_name: str = None):
        self.message = message
        self.router_name = router_name
        super().__init__(f"{router_name}: {message}" if router_name else message)


class ConnectionError(RouterError):
    """Exception raised for connection errors."""
    pass


class ExecutionError(RouterError):
    """Exception raised for command execution errors."""
    pass


class FlashError(RouterError):
    """Exception raised for firmware flashing errors."""
    pass
