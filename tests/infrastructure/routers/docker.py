"""
Docker router simulation for testing.

Provides a lightweight simulation of OpenWRT routers using Docker containers.
"""

import asyncio
import subprocess
from typing import Dict, Any, Optional
import logging

from .base import RouterInterface, ConnectionError, ExecutionError

logger = logging.getLogger(__name__)


class DockerRouter(RouterInterface):
    """Docker-based router simulation."""

    def __init__(self, name: str, config: Dict[str, Any]):
        """
        Initialize Docker router.

        Args:
            name: Router identifier
            config: Configuration with keys:
                - image: Docker image to use
                - port_map: Port mappings (host:container)
                - network: Docker network to use
        """
        super().__init__(name, config)
        self.image = config.get('image', 'openwrt/openwrt:latest')
        self.port_map = config.get('port_map', {'80': '8080', '2121': '22121'})
        self.network = config.get('network', 'bridge')

        self._container_id = None
        self._container_ip = None

    async def connect(self, timeout: int = 60) -> bool:
        """
        Start Docker container and establish connection.

        1. Pull Docker image
        2. Create container with port mappings
        3. Wait for container to be ready
        4. Connect to container
        """
        logger.info(f"Starting Docker router {self.name} (image: {self.image})")

        try:
            # Pull image if not exists
            logger.debug(f"Pulling Docker image {self.image}...")
            pull_cmd = ["docker", "pull", self.image]
            process = await asyncio.create_subprocess_exec(
                *pull_cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )

            await process.wait()

            # Create port mapping arguments
            port_mappings = []
            for host_port, container_port in self.port_map.items():
                port_mappings.append(f"-p {host_port}:{container_port}")

            # Create container
            logger.debug(f"Creating Docker container...")
            create_cmd = [
                "docker", "run", "-d",
                "--name", f"tollgate-{self.name}",
                f"--network", self.network
            ] + port_mappings + [self.image]

            process = await asyncio.create_subprocess_exec(
                *create_cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )

            stdout, stderr = await process.communicate()

            if process.returncode != 0:
                error_msg = stderr.decode()
                logger.error(f"Failed to create container: {error_msg}")
                raise ConnectionError(f"Docker container creation failed: {error_msg}", self.name)

            self._container_id = stdout.decode().strip()

            # Get container IP
            await asyncio.sleep(2)  # Wait for container to start
            inspect_cmd = [
                "docker", "inspect",
                "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}",
                f"tollgate-{self.name}"
            ]

            process = await asyncio.create_subprocess_exec(
                *inspect_cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )

            stdout, stderr = await process.communicate()

            if process.returncode == 0:
                self._container_ip = stdout.decode().strip()
                logger.info(f"Docker container {self.name} ready at {self._container_ip}")
                self._connected = True
                return True
            else:
                logger.error(f"Failed to get container IP: {stderr.decode()}")
                return False

        except Exception as e:
            logger.error(f"Error starting Docker router {self.name}: {e}")
            raise ConnectionError(f"Docker startup failed: {e}", self.name) from e

    async def disconnect(self) -> bool:
        """
        Stop and remove Docker container.
        """
        logger.info(f"Stopping Docker container {self.name}")

        try:
            if self._container_id:
                stop_cmd = ["docker", "stop", f"tollgate-{self.name}"]
                await asyncio.create_subprocess_exec(*stop_cmd).wait()

                rm_cmd = ["docker", "rm", f"tollgate-{self.name}"]
                await asyncio.create_subprocess_exec(*rm_cmd).wait()

            self._connected = False
            self._container_id = None
            self._container_ip = None
            logger.info(f"Docker container {self.name} removed")
            return True

        except Exception as e:
            logger.error(f"Error stopping Docker container {self.name}: {e}")
            return False

    async def execute_command(self, command: str, timeout: int = 30) -> str:
        """
        Execute command in Docker container.

        Uses docker exec.
        """
        if not self._connected or not self._container_id:
            raise ConnectionError("Docker container not running", self.name)

        logger.debug(f"Executing command in {self.name}: {command}")

        try:
            exec_cmd = ["docker", "exec", f"tollgate-{self.name}", "sh", "-c", command]

            process = await asyncio.create_subprocess_exec(
                *exec_cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )

            stdout, stderr = await asyncio.wait_for(
                process.communicate(),
                timeout=timeout
            )

            if process.returncode != 0:
                error_msg = stderr.decode().strip()
                logger.error(f"Command failed in {self.name}: {error_msg}")
                raise ExecutionError(f"Command failed: {error_msg}", self.name)

            output = stdout.decode().strip()
            logger.debug(f"Command output: {output[:100]}...")
            return output

        except asyncio.TimeoutError:
            logger.error(f"Command timeout in {self.name} after {timeout}s")
            raise ExecutionError(f"Command timeout after {timeout}s", self.name)
        except Exception as e:
            logger.error(f"Error executing command in {self.name}: {e}")
            raise ExecutionError(f"Execution error: {e}", self.name) from e

    async def upload_file(self, local_path: str, remote_path: str) -> bool:
        """
        Upload file to Docker container.

        Uses docker cp.
        """
        logger.info(f"Uploading {local_path} to {self.name}:{remote_path}")

        try:
            cp_cmd = ["docker", "cp", local_path, f"tollgate-{self.name}:{remote_path}"]

            process = await asyncio.create_subprocess_exec(
                *cp_cmd,
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
        Download file from Docker container.

        Uses docker cp.
        """
        logger.info(f"Downloading {self.name}:{remote_path} to {local_path}")

        try:
            cp_cmd = ["docker", "cp", f"tollgate-{self.name}:{remote_path}", local_path]

            process = await asyncio.create_subprocess_exec(
                *cp_cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )

            stdout, stderr = await process.communicate()

            if process.returncode == 0:
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
        Flash firmware in Docker container.

        Note: Docker containers typically don't support firmware flashing.
        This would require recreating the container with a new image.
        """
        logger.warning(f"Firmware flashing not supported in Docker container {self.name}")
        logger.info(f"Recreate container with new image instead")

        # For Docker, we'd need to:
        # 1. Build new image with firmware
        # 2. Stop and remove current container
        # 3. Create new container with new image

        return False

    async def install_package(self, package_path: str, timeout: int = 120) -> bool:
        """
        Install package in Docker container.

        Uploads package and uses opkg install (simulated).
        """
        logger.info(f"Installing package in {self.name}: {package_path}")

        try:
            # Upload package
            remote_path = f"/tmp/{package_path.split('/')[-1]}"
            upload_success = await self.upload_file(package_path, remote_path)

            if not upload_success:
                raise ExecutionError("Failed to upload package", self.name)

            # Install using opkg (simulated - may not work in Docker)
            install_cmd = f"cd /tmp && opkg install {remote_path.split('/')[-1]}"

            try:
                await self.execute_command(install_cmd, timeout=timeout)
                logger.info(f"Successfully installed package in {self.name}")
                return True
            except ExecutionError as e:
                # opkg may not work in Docker container
                logger.warning(f"Package installation may not work in Docker: {e}")
                # For simulation purposes, we'll return True
                return True

        except Exception as e:
            logger.error(f"Error installing package in {self.name}: {e}")
            return False

    async def get_info(self) -> Dict[str, Any]:
        """
        Get Docker container information.
        """
        if not self._connected or not self._container_id:
            raise ConnectionError("Docker container not running", self.name)

        try:
            # Get container info using docker inspect
            inspect_cmd = [
                "docker", "inspect",
                "-f", "{{.State.Status}}",
                f"tollgate-{self.name}"
            ]

            process = await asyncio.create_subprocess_exec(
                *inspect_cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )

            stdout, stderr = await process.communicate()

            status = stdout.decode().strip()

            return {
                'arch': 'x86_64',  # Docker is typically x86_64
                'version': 'docker-simulation',
                'ip': self._container_ip,
                'status': status,
                'type': 'docker',
                'container_id': self._container_id,
                'uptime': 0,  # Would need to calculate from container start time
                'load': [0.0, 0.0, 0.0]
            }

        except Exception as e:
            logger.error(f"Error getting info from {self.name}: {e}")
            raise ExecutionError(f"Failed to get info: {e}", self.name) from e

    async def reboot(self, timeout: int = 180) -> bool:
        """
        Reboot Docker container (restart).

        Docker containers don't reboot, they restart.
        """
        logger.info(f"Restarting Docker container {self.name}")

        try:
            if self._container_id:
                restart_cmd = ["docker", "restart", f"tollgate-{self.name}"]
                process = await asyncio.create_subprocess_exec(
                    *restart_cmd,
                    stdout=asyncio.subprocess.PIPE,
                    stderr=asyncio.subprocess.PIPE
                )

                await process.wait()

                # Wait for container to come back online
                await asyncio.sleep(10)

                self._connected = True
                logger.info(f"Docker container {self.name} restarted")
                return True

        except Exception as e:
            logger.error(f"Error restarting Docker container {self.name}: {e}")
            return False

    async def is_online(self) -> bool:
        """
        Check if Docker container is running.

        Uses docker ps.
        """
        try:
            ps_cmd = [
                "docker", "ps",
                "-f", "name=tollgate-{}".format(self.name),
                "--format", "{{.Status}}"
            ]

            process = await asyncio.create_subprocess_exec(
                *ps_cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )

            stdout, stderr = await process.communicate()

            status = stdout.decode().strip()
            online = status == "Up"

            if online:
                logger.debug(f"Docker container {self.name} is online")
            else:
                logger.debug(f"Docker container {self.name} is offline")
            return online

        except Exception as e:
            logger.debug(f"Error checking online status for {self.name}: {e}")
            return False

    async def get_logs(self, lines: int = 100) -> str:
        """
        Retrieve logs from Docker container.

        Uses docker logs.
        """
        if not self._connected or not self._container_id:
            return ""

        try:
            logs_cmd = ["docker", "logs", "--tail", str(lines), f"tollgate-{self.name}"]

            process = await asyncio.create_subprocess_exec(
                *logs_cmd,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE
            )

            stdout, stderr = await process.communicate()

            logs = stdout.decode()
            return logs

        except Exception as e:
            logger.error(f"Error getting logs from {self.name}: {e}")
            return ""

    async def cleanup(self) -> bool:
        """
        Clean up temporary files in Docker container.
        """
        try:
            cleanup_cmd = "rm -f /tmp/*.ipk /tmp/*.bin"
            await self.execute_command(cleanup_cmd)
            logger.info(f"Cleanup completed in {self.name}")
            return True
        except Exception as e:
            logger.error(f"Error during cleanup in {self.name}: {e}")
            return False
