"""
Router pool management for parallel test execution.

Manages a pool of routers and coordinates test execution.
"""

import asyncio
from typing import Dict, List, Optional, Callable, Any
import logging
from datetime import datetime

from .routers.base import RouterInterface, RouterError

logger = logging.getLogger(__name__)


class RouterPool:
    """Pool of routers for parallel test execution."""

    def __init__(self, routers: List[RouterInterface], max_parallel: int = 3):
        """
        Initialize router pool.

        Args:
            routers: List of router instances
            max_parallel: Maximum number of routers to use simultaneously
        """
        self.routers = routers
        self.max_parallel = max_parallel
        self.router_status: Dict[str, Dict[str, Any]] = {}

        # Initialize status for each router
        for router in self.routers:
            self.router_status[router.name] = {
                'available': True,
                'last_used': None,
                'test_results': [],
                'errors': []
            }

        logger.info(f"Initialized router pool with {len(routers)} routers (max parallel: {max_parallel})")

    async def execute_on_pool(
        self,
        test_func: Callable[[RouterInterface], Any],
        router_filter: Optional[List[str]] = None,
        tags: Optional[List[str]] = None
    ) -> Dict[str, Any]:
        """
        Execute test function on router pool.

        Args:
            test_func: Async function to execute on each router
            router_filter: List of router names to use (None for all)
            tags: List of tags to filter by (None for all)

        Returns:
            Dictionary with results from all routers
        """
        # Filter routers
        target_routers = self._filter_routers(router_filter, tags)

        if not target_routers:
            logger.warning("No routers matched filters")
            return {}

        logger.info(f"Executing test on {len(target_routers)} routers")

        # Execute tests in parallel (respecting max_parallel limit)
        results = {}

        if self.max_parallel == 1:
            # Sequential execution
            for router in target_routers:
                result = await self._execute_on_router(router, test_func)
                results[router.name] = result
        else:
            # Parallel execution with semaphore
            semaphore = asyncio.Semaphore(self.max_parallel)

            async def execute_with_semaphore(router):
                async with semaphore:
                    return await self._execute_on_router(router, test_func)

            # Create tasks
            tasks = [execute_with_semaphore(router) for router in target_routers]

            # Wait for all tasks to complete
            task_results = await asyncio.gather(*tasks, return_exceptions=True)

            # Collect results
            for router, result in zip(target_routers, task_results):
                if isinstance(result, Exception):
                    results[router.name] = {
                        'success': False,
                        'error': str(result),
                        'timestamp': datetime.now().isoformat()
                    }
                else:
                    results[router.name] = result

        return results

    async def _execute_on_router(
        self,
        router: RouterInterface,
        test_func: Callable[[RouterInterface], Any]
    ) -> Dict[str, Any]:
        """
        Execute test function on single router.

        Args:
            router: Router instance
            test_func: Test function to execute

        Returns:
            Test result dictionary
        """
        logger.info(f"Executing test on {router.name}")

        status = self.router_status.get(router.name, {})

        # Update status
        status['available'] = False
        status['last_used'] = datetime.now().isoformat()

        try:
            # Connect to router
            connect_timeout = 60
            if not await router.connect(timeout=connect_timeout):
                raise RouterError(f"Failed to connect to router", router.name)

            # Execute test function
            start_time = datetime.now()
            result = await test_func(router)
            end_time = datetime.now()

            # Update router status
            status['test_results'].append({
                'test': test_func.__name__,
                'success': True,
                'timestamp': end_time.isoformat(),
                'duration': (end_time - start_time).total_seconds()
            })

            logger.info(f"Test completed successfully on {router.name}")

            # Disconnect
            await router.disconnect()

            return {
                'success': True,
                'result': result,
                'timestamp': end_time.isoformat(),
                'duration': (end_time - start_time).total_seconds(),
                'router': router.name
            }

        except Exception as e:
            # Handle errors
            error_time = datetime.now()

            status['errors'].append({
                'error': str(e),
                'timestamp': error_time.isoformat()
            })

            logger.error(f"Test failed on {router.name}: {e}")

            # Ensure router is disconnected
            try:
                await router.disconnect()
            except:
                pass

            return {
                'success': False,
                'error': str(e),
                'timestamp': error_time.isoformat(),
                'router': router.name
            }

        finally:
            # Mark router as available again
            status['available'] = True

    def _filter_routers(
        self,
        names: Optional[List[str]] = None,
        tags: Optional[List[str]] = None
    ) -> List[RouterInterface]:
        """
        Filter routers by name or tags.

        Args:
            names: List of router names (None for all)
            tags: List of tags (None for all)

        Returns:
            Filtered list of routers
        """
        filtered = self.routers

        if names:
            filtered = [r for r in filtered if r.name in names]
            logger.debug(f"Filtered by names: {[r.name for r in filtered]}")

        if tags:
            filtered = [r for r in filtered if self._matches_tags(r, tags)]
            logger.debug(f"Filtered by tags: {tags}")

        return filtered

    def _matches_tags(self, router: RouterInterface, tags: List[str]) -> bool:
        """
        Check if router matches specified tags.

        Args:
            router: Router instance
            tags: Tags to match

        Returns:
            True if all tags match
        """
        router_tags = router.tags
        return all(tag in router_tags for tag in tags)

    def get_router_status(self, router_name: str = None) -> Dict[str, Any]:
        """
        Get status of router(s).

        Args:
            router_name: Specific router name (None for all)

        Returns:
            Status dictionary
        """
        if router_name:
            return self.router_status.get(router_name, {})
        return self.router_status

    def get_summary(self) -> Dict[str, Any]:
        """
        Get pool execution summary.

        Returns:
            Summary dictionary
        """
        total_tests = sum(
            len(status.get('test_results', []))
            for status in self.router_status.values()
        )

        successful_tests = sum(
            len([r for r in status.get('test_results', []) if r.get('success', False)])
            for status in self.router_status.values()
        )

        total_errors = sum(
            len(status.get('errors', []))
            for status in self.router_status.values()
        )

        return {
            'total_routers': len(self.routers),
            'total_tests': total_tests,
            'successful_tests': successful_tests,
            'failed_tests': total_tests - successful_tests,
            'total_errors': total_errors,
            'success_rate': (successful_tests / total_tests * 100) if total_tests > 0 else 0,
            'routers': {
                name: {
                    'tests_executed': len(status.get('test_results', [])),
                    'errors': len(status.get('errors', [])),
                    'last_used': status.get('last_used')
                }
                for name, status in self.router_status.items()
            }
        }

    async def cleanup(self) -> bool:
        """
        Cleanup all routers in pool.

        Returns:
            True if all cleanups successful
        """
        logger.info("Cleaning up router pool")

        cleanup_tasks = [router.cleanup() for router in self.routers]

        if cleanup_tasks:
            results = await asyncio.gather(*cleanup_tasks, return_exceptions=True)

            # Count successful cleanups
            successful = sum(1 for r in results if r is True)

            logger.info(f"Cleanup completed: {successful}/{len(self.routers)} routers")

            return successful == len(self.routers)

        return True
