# Terway Controlplane Degradation Design

## Overview

This document describes the degradation mechanism in terway-controlplane. Degradation is a feature that allows the system to continue operating in a limited capacity when the cloud provider's OpenAPI is unavailable or experiencing issues.

The degradation feature is only effective when `centralizedIPAM=true` is enabled in Terway configuration.

## Degradation Levels

### L0 Degradation Mode

In L0 degradation mode, all OpenAPI requests are stopped. Terway will completely stop interacting with OpenAPI and only use cached ENIs and IPs for pod networking.

This mode provides maximum stability when the cloud provider API is completely unavailable, but at the cost of flexibility in resource allocation.

### L1 Degradation Mode

In L1 degradation mode, Terway stops making OpenAPI requests that release resources, but continues to make requests that allocate resources.

This mode allows the system to continue creating new pods while preventing the accidental release of resources during API instability.

## Configuration

To enable degradation, set the following in the terway-controlplane configuration:
