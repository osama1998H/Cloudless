# Cloudless Platform

[![CI](https://github.com/osama1998H/Cloudless/actions/workflows/ci.yml/badge.svg)](https://github.com/osama1998H/Cloudless/actions/workflows/ci.yml)
[![Release](https://github.com/osama1998H/Cloudless/actions/workflows/release.yml/badge.svg)](https://github.com/osama1998H/Cloudless/actions/workflows/release.yml)
[![CodeQL](https://github.com/osama1998H/Cloudless/actions/workflows/codeql.yml/badge.svg)](https://github.com/osama1998H/Cloudless/actions/workflows/codeql.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/osama1998H/Cloudless)](https://goreportcard.com/report/github.com/osama1998H/Cloudless)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/osama1998H/Cloudless)](go.mod)

A distributed compute platform that unifies heterogeneous devices (phones, laptops, PCs, IoT gateways, VPS) into a single elastic compute fabric.

## Overview

Cloudless aggregates surplus compute capacity from personal and edge devices, presenting it as a dependable pool for running containerized applications with high availability and graceful churn handling.

## Architecture

The platform consists of three main planes:

- **Control Plane**: Coordinator, Scheduler, Metadata Store (RAFT), API Gateway
- **Data Plane**: Node Agent, Resource Monitor, Overlay Networking, Storage Engine
- **Observability Plane**: Metrics, logs, traces, event stream

## Quick Start

### Prerequisites

- Go 1.23+
- Docker and Docker Compose
- Protocol Buffers compiler (protoc)

### Building

```bash
# Clone the repository
git clone https://github.com/cloudless/cloudless.git
cd cloudless

# Install dependencies
make mod

# Generate protobuf code
make proto

# Build all binaries
make build

# Run tests
make test
```

### Running Locally

```bash
# Start local development cluster with Docker Compose
make compose-up

# View logs
make compose-logs

# Stop cluster
make compose-down
```

### Running Coordinator

```bash
# Start coordinator (on VPS anchor)
./build/coordinator \
  --data-dir=/var/lib/cloudless \
  --bind-addr=0.0.0.0:8080 \
  --raft-bootstrap=true
```

### Running Agent

```bash
# Start agent (on each node)
./build/agent \
  --coordinator-addr=coordinator:8080 \
  --node-name=node-1 \
  --region=us-west \
  --zone=us-west-1a
```

## Development

### Project Structure

```
.
├── cmd/
│   ├── coordinator/    # Coordinator entry point
│   └── agent/          # Agent entry point
├── pkg/
│   ├── api/           # Protobuf/gRPC definitions
│   ├── coordinator/   # Coordinator implementation
│   ├── agent/         # Agent implementation
│   ├── scheduler/     # Scheduling logic
│   ├── overlay/       # Network overlay (QUIC)
│   ├── storage/       # Distributed object store
│   ├── raft/          # RAFT consensus
│   ├── policy/        # Security policies
│   └── observability/ # Metrics, logging, tracing
├── deployments/
│   └── docker/        # Docker configurations
├── config/            # Configuration examples
├── examples/          # Example workloads
└── test/             # Integration and chaos tests
```

### Testing

```bash
# Run unit tests
make test

# Run tests with race detector
make test-race

# Run integration tests
make test-integration

# Run chaos tests
make test-chaos

# Generate coverage report
make coverage
```

### Contributing

Please see [CONTRIBUTING.md](CONTRIBUTING.md) for development guidelines.

## Features

### Current (v0.1 MVP)

- [x] Secure node enrollment with mTLS
- [x] Basic workload scheduling
- [x] Container runtime integration (containerd)
- [x] QUIC-based overlay networking
- [x] Simple replicated storage (R=2)
- [x] Prometheus metrics
- [x] CLI and gRPC API

### Roadmap

- [ ] Advanced scheduling with locality awareness
- [ ] Erasure coding for storage
- [ ] GPU support
- [ ] Windows/macOS agents
- [ ] Web UI dashboard
- [ ] Multi-tenant support
- [ ] Billing and marketplace

## Performance Targets

- Scheduler decisions: 200ms P50, 800ms P95 (5k nodes)
- Membership convergence: 5s P50, 15s P95
- Failed replica rescheduling: 3s P50, 10s P95
- Coordinator availability: 99.9% monthly

## Security

- All communication uses mTLS with rotating certificates
- SPIFFE-like workload identities
- OPA-style policy engine for access control
- Encrypted storage at rest
- Sandboxed container execution

## Documentation

- [Architecture Overview](docs/architecture.md)
- [API Reference](docs/api.md)
- [Operations Guide](docs/operations.md)
- [Security Model](docs/security.md)

## License

Copyright (c) 2024 Cloudless Project. All rights reserved.

## Support

For issues and questions:
- GitHub Issues: [github.com/cloudless/cloudless/issues](https://github.com/cloudless/cloudless/issues)
- Documentation: [docs.cloudless.io](https://docs.cloudless.io)