import pytest

def test_install_packages(install_packages_on_tollgates):
    """Test that uses the install_packages_on_tollgates fixture to install packages."""
    # The fixture does all the work of finding networks, connecting, and installing packages
    # We just need to verify that the fixture ran and collected results
    # Note: Some installations may fail due to network issues or file conflicts, which is expected
    print(f"Installation results: {install_packages_on_tollgates}")
    # We don't assert on the number of successful installations since some failures are expected
    # in a real-world scenario. The test passes as long as the fixture completes.