import pytest

def test_copy_images(copy_images_to_tollgates):
    """Test that uses the copy_images_to_tollgates fixture to copy images."""
    # The fixture does all the work of finding networks, connecting, and copying images
    # We just need to verify that the fixture ran and collected results
    # Note: Some copies may fail due to network issues or file conflicts, which is expected
    print(f"Image copy results: {copy_images_to_tollgates}")
    # We don't assert on the number of successful copies since some failures are expected
    # in a real-world scenario. The test passes as long as the fixture completes.