"""
Firmware deployment automation for TollGate testing infrastructure.
"""

import os
import asyncio
import logging
from typing import Dict, Any, Optional, List
from pathlib import Path

from ..routers.base import RouterInterface, FlashError

logger = logging.getLogger(__name__)


class FirmwareDeployer:
    """Automates firmware deployment to routers."""

    def __init__(self, config: Dict[str, Any]):
        """
        Initialize firmware deployer.

        Args:
            config: Resources configuration from test_config.yml
        """
        self.firmware_base_path = config.get('base_path', './firmware')
        self.current_version = config.get('current_version', '0.0.4')
        self.firmware_file = None

        # Find firmware file
        self._locate_firmware()

    def _locate_firmware(self) -> None:
        """
        Locate firmware file in base path.

        Looks for .bin files with version in name.
        """
        logger.info(f"Locating firmware for version {self.current_version}")

        firmware_path = Path(self.firmware_base_path)

        if not firmware_path.exists():
            logger.error(f"Firmware base path does not exist: {self.firmware_base_path}")
            raise FileNotFoundError(f"Firmware directory not found: {self.firmware_base_path}")

        # Search for firmware file
        firmware_files = list(firmware_path.glob(f"*{self.current_version}*.bin"))

        if not firmware_files:
            logger.warning(f"No firmware file found for version {self.current_version}")
            logger.info("Searching for any .bin files...")
            firmware_files = list(firmware_path.glob("*.bin"))

        if firmware_files:
            # Use the most recently modified file
            self.firmware_file = max(firmware_files, key=lambda f: f.stat().st_mtime)
            logger.info(f"Found firmware file: {self.firmware_file.name}")
        else:
            logger.error(f"No firmware files found in {self.firmware_base_path}")
            raise FileNotFoundError(f"No firmware files found")

    async def deploy_to_router(
        self,
        router: RouterInterface,
        timeout: int = 300
    ) -> Dict[str, Any]:
        """
        Deploy firmware to router.

        Args:
            router: Router instance
            timeout: Flash timeout in seconds

        Returns:
            Deployment result dictionary
        """
        if not self.firmware_file:
            raise FileNotFoundError("No firmware file located for deployment")

        logger.info(f"Deploying firmware {self.firmware_file.name} to {router.name}")

        result = {
            'router': router.name,
            'firmware': self.firmware_file.name,
            'version': self.current_version,
            'success': False,
            'error': None,
            'duration': 0
        }

        try:
            import time
            start_time = time.time()

            # Upload firmware to router
            upload_success = await router.upload_file(
                str(self.firmware_file),
                f"/tmp/{self.firmware_file.name}"
            )

            if not upload_success:
                result['error'] = 'Firmware upload failed'
                return result

            # Flash firmware on router
            flash_success = await router.install_firmware(
                f"/tmp/{self.firmware_file.name}",
                timeout=timeout
            )

            end_time = time.time()
            result['duration'] = end_time - start_time

            if flash_success:
                result['success'] = True
                logger.info(f"Firmware deployment successful on {router.name}")
            else:
                result['error'] = 'Firmware flash failed'

            return result

        except FlashError as e:
            logger.error(f"Firmware deployment failed on {router.name}: {e.message}")
            result['error'] = e.message
            return result

        except Exception as e:
            logger.error(f"Unexpected error during firmware deployment on {router.name}: {e}")
            result['error'] = str(e)
            return result

    async def deploy_to_pool(
        self,
        routers: List[RouterInterface],
        parallel: bool = True,
        max_parallel: int = 3
    ) -> Dict[str, Any]:
        """
        Deploy firmware to router pool.

        Args:
            routers: List of router instances
            parallel: Whether to deploy in parallel (default: True)
            max_parallel: Maximum parallel deployments

        Returns:
            Deployment summary dictionary
        """
        logger.info(f"Deploying firmware to {len(routers)} routers")

        results = {}

        if not parallel or max_parallel == 1:
            # Sequential deployment
            for router in routers:
                result = await self.deploy_to_router(router)
                results[router.name] = result
        else:
            # Parallel deployment
            semaphore = asyncio.Semaphore(max_parallel)

            async def deploy_with_semaphore(router):
                async with semaphore:
                    return await self.deploy_to_router(router)

            tasks = [deploy_with_semaphore(router) for router in routers]
            task_results = await asyncio.gather(*tasks, return_exceptions=True)

            # Collect results
            for router, result in zip(routers, task_results):
                if isinstance(result, Exception):
                    results[router.name] = {
                        'success': False,
                        'error': str(result),
                        'firmware': self.firmware_file.name if self.firmware_file else 'unknown'
                    }
                else:
                    results[router.name] = result

        # Generate summary
        successful = sum(1 for r in results.values() if r.get('success', False))
        failed = len(results) - successful

        return {
            'total_routers': len(routers),
            'successful': successful,
            'failed': failed,
            'success_rate': (successful / len(routers) * 100) if routers else 0,
            'results': results
        }


class PackageDeployer:
    """Automates package deployment to routers."""

    def __init__(self, config: Dict[str, Any]):
        """
        Initialize package deployer.

        Args:
            config: Resources configuration from test_config.yml
        """
        self.base_url = config.get('repository_url', '')
        self.local_path = config.get('local_path', './packages')
        self.package_file = None

        # Locate package file
        self._locate_package()

    def _locate_package(self) -> None:
        """
        Locate package file for deployment.

        Looks for .ipk files.
        """
        logger.info("Locating TollGate package")

        package_path = Path(self.local_path)

        if not package_path.exists():
            logger.warning(f"Package path does not exist: {self.local_path}")
            logger.info("Will download package from repository if needed")

        # Search for local package file
        package_files = list(package_path.glob("*.ipk"))

        if package_files:
            # Use the most recently modified file
            self.package_file = max(package_files, key=lambda f: f.stat().st_mtime)
            logger.info(f"Found package file: {self.package_file.name}")
        else:
            logger.info("No local package file found - will download from repository")

    async def deploy_to_router(
        self,
        router: RouterInterface,
        timeout: int = 120
    ) -> Dict[str, Any]:
        """
        Deploy package to router.

        Args:
            router: Router instance
            timeout: Installation timeout in seconds

        Returns:
            Deployment result dictionary
        """
        if self.package_file:
            package_path = str(self.package_file)
            logger.info(f"Deploying local package {self.package_file.name} to {router.name}")
        else:
            # Generate package URL
            package_url = f"{self.base_url}/tollgate-wrt_latest.ipk"
            package_path = package_url
            logger.info(f"Deploying remote package to {router.name}")

        result = {
            'router': router.name,
            'package': package_path,
            'success': False,
            'error': None,
            'duration': 0
        }

        try:
            import time
            start_time = time.time()

            if self.package_file:
                # Upload local package
                upload_success = await router.upload_file(
                    package_path,
                    f"/tmp/{self.package_file.name}"
                )

                if not upload_success:
                    result['error'] = 'Package upload failed'
                    return result
            else:
                # Download package on router using wget
                download_cmd = f"cd /tmp && wget {package_url}"
                try:
                    await router.execute_command(download_cmd, timeout=60)
                except Exception as e:
                    result['error'] = f'Package download failed: {e}'
                    return result

                # Update package path
                package_path = f"/tmp/{package_url.split('/')[-1]}"

            # Install package
            install_success = await router.install_package(package_path, timeout=timeout)

            end_time = time.time()
            result['duration'] = end_time - start_time

            if install_success:
                result['success'] = True
                logger.info(f"Package deployment successful on {router.name}")
            else:
                result['error'] = 'Package installation failed'

            return result

        except Exception as e:
            logger.error(f"Unexpected error during package deployment on {router.name}: {e}")
            result['error'] = str(e)
            return result

    async def deploy_to_pool(
        self,
        routers: List[RouterInterface],
        parallel: bool = True,
        max_parallel: int = 3
    ) -> Dict[str, Any]:
        """
        Deploy package to router pool.

        Args:
            routers: List of router instances
            parallel: Whether to deploy in parallel (default: True)
            max_parallel: Maximum parallel deployments

        Returns:
            Deployment summary dictionary
        """
        logger.info(f"Deploying package to {len(routers)} routers")

        results = {}

        if not parallel or max_parallel == 1:
            # Sequential deployment
            for router in routers:
                result = await self.deploy_to_router(router)
                results[router.name] = result
        else:
            # Parallel deployment
            semaphore = asyncio.Semaphore(max_parallel)

            async def deploy_with_semaphore(router):
                async with semaphore:
                    return await self.deploy_to_router(router)

            tasks = [deploy_with_semaphore(router) for router in routers]
            task_results = await asyncio.gather(*tasks, return_exceptions=True)

            # Collect results
            for router, result in zip(routers, task_results):
                if isinstance(result, Exception):
                    results[router.name] = {
                        'success': False,
                        'error': str(result)
                    }
                else:
                    results[router.name] = result

        # Generate summary
        successful = sum(1 for r in results.values() if r.get('success', False))
        failed = len(results) - successful

        return {
            'total_routers': len(routers),
            'successful': successful,
            'failed': failed,
            'success_rate': (successful / len(routers) * 100) if routers else 0,
            'results': results
        }
