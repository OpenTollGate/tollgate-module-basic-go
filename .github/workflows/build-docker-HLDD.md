# Build Docker Workflow High-Level Design Document

## Overview

The Build Docker workflow is a GitHub Actions workflow responsible for building and publishing the TollGate module package. It determines the package version and release channel based on the Git reference, builds the package using the OpenWRT SDK, and publishes the package metadata to Nostr relays.

## Responsibilities

- Determine the package version and release channel based on the Git reference.
- Build the package using the OpenWRT SDK.
- Publish the package metadata to Nostr relays.

## Inputs

- Git reference (push event)

## Outputs

- Package version
- Release channel
- Package metadata published to Nostr relays

## Interfaces

- GitHub Actions workflow
- OpenWRT SDK
- Nostr relays

## Pending Tasks

- Include the timestamp at the time of running the workflow as an environment variable passed to the Makefile.
- Update the Makefile to distinguish between dev and stable channels and generate version numbers accordingly.