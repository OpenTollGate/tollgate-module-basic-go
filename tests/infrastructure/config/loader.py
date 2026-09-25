"""
Configuration loader for test infrastructure.

Loads and validates test configuration from YAML files.
"""

import os
import yaml
from typing import Dict, Any, List, Optional
import logging

logger = logging.getLogger(__name__)


class TestConfig:
    """Test configuration loader and validator."""

    def __init__(self, config_path: str = None):
        """
        Load test configuration.

        Args:
            config_path: Path to config file (default: ./infrastructure/config/test_config.yml)
        """
        if config_path is None:
            # Default config path
            config_path = os.path.join(
                os.path.dirname(__file__),
                'config',
                'test_config.yml'
            )

        self.config_path = config_path
        self.config = self._load_config(config_path)
        self._validate_config()

    def _load_config(self, path: str) -> Dict[str, Any]:
        """
        Load YAML configuration file.

        Args:
            path: Path to YAML file

        Returns:
            Configuration dictionary
        """
        logger.info(f"Loading test configuration from {path}")

        try:
            with open(path, 'r') as f:
                config = yaml.safe_load(f)

            # Expand environment variables
            config = self._expand_env_vars(config)

            return config

        except FileNotFoundError:
            logger.error(f"Configuration file not found: {path}")
            raise
        except yaml.YAMLError as e:
            logger.error(f"Error parsing YAML configuration: {e}")
            raise

    def _expand_env_vars(self, config: Any) -> Any:
        """
        Recursively expand environment variables in configuration.

        Args:
            config: Configuration value

        Returns:
            Configuration with environment variables expanded
        """
        if isinstance(config, str):
            # Expand ${VAR} or ${VAR:default} syntax
            if config.startswith('${') and config.endswith('}'):
                var_spec = config[2:-1]
                if ':' in var_spec:
                    var_name, default = var_spec.split(':', 1)
                    value = os.environ.get(var_name, default)
                else:
                    value = os.environ.get(var_spec, '')
                return value
            return config

        elif isinstance(config, dict):
            return {k: self._expand_env_vars(v) for k, v in config.items()}

        elif isinstance(config, list):
            return [self._expand_env_vars(item) for item in config]

        return config

    def _validate_config(self) -> None:
        """
        Validate configuration structure.

        Raises ValueError if configuration is invalid.
        """
        logger.info("Validating test configuration")

        # Check required sections
        required_sections = ['routers', 'testing']
        for section in required_sections:
            if section not in self.config:
                raise ValueError(f"Missing required section: {section}")

        # Check routers configuration
        routers_config = self.config.get('routers', {})

        if not any(routers_config.values()):
            logger.warning("No routers configured")

        # Validate physical routers
        for router in routers_config.get('physical', []):
            self._validate_router_config(router, 'physical')

        # Validate cloud routers
        for router in routers_config.get('cloud', {}).get('servers', []):
            self._validate_router_config(router, 'cloud')

        # Validate docker routers
        for router in routers_config.get('docker', {}).get('containers', []):
            self._validate_router_config(router, 'docker')

        logger.info("Configuration validation passed")

    def _validate_router_config(self, router: Dict[str, Any], router_type: str) -> None:
        """
        Validate individual router configuration.

        Args:
            router: Router configuration
            router_type: Type of router (physical, cloud, docker)
        """
        name = router.get('name', 'unknown')
        required_fields = {
            'physical': ['host', 'password'],
            'cloud': ['api_token'],
            'docker': ['image']
        }

        for field in required_fields.get(router_type, []):
            if field not in router:
                logger.warning(f"Router {name} missing required field: {field}")

    def get_routers(self, router_type: str = None, tags: List[str] = None) -> List[Dict[str, Any]]:
        """
        Get router configurations.

        Args:
            router_type: Filter by type (physical, cloud, docker, None for all)
            tags: Filter by tags (None for all)

        Returns:
            List of router configurations
        """
        routers_config = self.config.get('routers', {})
        routers = []

        # Get physical routers
        if router_type in (None, 'physical'):
            for router in routers_config.get('physical', []):
                if self._matches_tags(router, tags):
                    routers.append({'type': 'physical', **router})

        # Get cloud routers
        if router_type in (None, 'cloud'):
            cloud_config = routers_config.get('cloud', {})
            for router in cloud_config.get('servers', []):
                if self._matches_tags(router, tags):
                    routers.append({'type': 'cloud', **router})

        # Get docker routers
        if router_type in (None, 'docker'):
            docker_config = routers_config.get('docker', {})
            for router in docker_config.get('containers', []):
                if self._matches_tags(router, tags):
                    routers.append({'type': 'docker', **router})

        return routers

    def _matches_tags(self, router: Dict[str, Any], tags: List[str]) -> bool:
        """
        Check if router matches specified tags.

        Args:
            router: Router configuration
            tags: Tags to match (None for all)

        Returns:
            True if tags match or tags is None
        """
        if tags is None:
            return True

        router_tags = router.get('tags', [])

        # Check if all specified tags are in router tags
        return all(tag in router_tags for tag in tags)

    def get_testing_config(self) -> Dict[str, Any]:
        """
        Get testing configuration.

        Returns:
            Testing configuration dictionary
        """
        return self.config.get('testing', {})

    def get_resources_config(self) -> Dict[str, Any]:
        """
        Get resources configuration.

        Returns:
            Resources configuration dictionary
        """
        return self.config.get('resources', {})

    def get_mint_config(self) -> Dict[str, Any]:
        """
        Get mint configuration.

        Returns:
            Mint configuration dictionary
        """
        return self.config.get('mint', {})

    def get_reporting_config(self) -> Dict[str, Any]:
        """
        Get reporting configuration.

        Returns:
            Reporting configuration dictionary
        """
        return self.config.get('reporting', {})

    def get_logging_config(self) -> Dict[str, Any]:
        """
        Get logging configuration.

        Returns:
            Logging configuration dictionary
        """
        return self.config.get('logging', {})

    @property
    def parallel(self) -> bool:
        """Check if parallel testing is enabled."""
        return self.config.get('testing', {}).get('parallel', False)

    @property
    def max_parallel_routers(self) -> int:
        """Get maximum number of parallel routers."""
        return self.config.get('testing', {}).get('max_parallel_routers', 1)

    @property
    def timeouts(self) -> Dict[str, int]:
        """Get timeout configuration."""
        return self.config.get('testing', {}).get('timeouts', {})

    @property
    def retries(self) -> Dict[str, int]:
        """Get retry configuration."""
        return self.config.get('testing', {}).get('retries', {})
