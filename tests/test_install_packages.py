import pytest

def test_install_packages(install_packages_on_tollgates):
    """Dummy test that uses the install_packages_on_tollgates fixture."""
    # The fixture does all the work
    # We just need to assert that the fixture ran successfully
    assert len(install_packages_on_tollgates) > 0, "No packages were installed"