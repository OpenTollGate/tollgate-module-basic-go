"""
Router factory for creating router instances.

Provides a unified interface for creating different router types.
"""

from typing import Dict, Any
import logging

from .base import RouterInterface, RouterError
from .physical import PhysicalRouter
from .cloud import CloudRouter
from .docker import DockerRouter

logger = logging.getLogger(__name__)


class RouterFactory:
    """Factory for creating router instances."""

    @staticmethod
    def create_router(router_config: Dict[str, Any]) -> RouterInterface:
        """
        Create router instance based on configuration.

        Args:
            router_config: Router configuration dictionary with 'type' field

        Returns:
            RouterInterface instance

        Raises:
            ValueError: If router type is invalid
        """
        router_type = router_config.get('type', 'unknown')
        name = router_config.get('name', 'unknown')

        logger.info(f"Creating router {name} (type: {router_type})")

        router_classes = {
            'physical': PhysicalRouter,
            'cloud': CloudRouter,
            'docker': DockerRouter
        }

        router_class = router_classes.get(router_type)

        if not router_class:
            valid_types = ', '.join(router_classes.keys())
            raise ValueError(
                f"Invalid router type '{router_type}'. Valid types: {valid_types}"
            )

        try:
            return router_class(name, router_config)

        except Exception as e:
            logger.error(f"Failed to create router {name}: {e}")
            raise RouterError(f"Router creation failed: {e}", name) from e

    @staticmethod
    def create_routers(router_configs: list) -> list[RouterInterface]:
        """
        Create multiple router instances.

        Args:
            router_configs: List of router configuration dictionaries

        Returns:
            List of RouterInterface instances
        """
        routers = []

        for config in router_configs:
            try:
                router = RouterFactory.create_router(config)
                routers.append(router)
            except RouterError as e:
                logger.warning(f"Skipping router due to error: {e.message}")
                continue

        logger.info(f"Created {len(routers)} routers successfully")
        return routers
