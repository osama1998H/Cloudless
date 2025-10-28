# CI/CD Pipeline Documentation

This document describes the Continuous Integration and Continuous Deployment (CI/CD) pipeline for Cloudless.

## Overview

The Cloudless project uses GitHub Actions for automated testing, building, and releasing. The pipeline is designed to be simple, fast, and reliable.

## Workflows

### 1. CI Workflow (`.github/workflows/ci.yml`)

**Triggers:**
- Push to `main` branch
- Pull requests to `main` branch

**Jobs:**
1. **Lint** - Code quality checks
   - `golangci-lint` v1.62.2 with 5-minute timeout
   - Go formatting check (`gofmt`)
   - Go vet static analysis
   - Duration: ~1-2 minutes

2. **Test** - Unit tests
   - Runs `make test` with coverage
   - Uploads coverage to Codecov (optional)
   - Duration: ~2-3 minutes

3. **Build** - Binary compilation
   - Builds coordinator, agent, and CLI binaries
   - Uploads artifacts for 7 days retention
   - Duration: ~1-2 minutes

4. **Docker** - Container images
   - Builds coordinator and agent Docker images
   - Uses Docker Buildx with GitHub Actions cache
   - Images tagged as `cloudless/coordinator:ci` and `cloudless/agent:ci`
   - Duration: ~2-3 minutes

**Total Duration:** ~5-8 minutes

**Parallel Execution:** Lint and Test run in parallel; Build and Docker run after both complete.

### 2. Release Workflow (`.github/workflows/release.yml`)

**Triggers:**
- Git tags matching `v*.*.*` (e.g., `v1.0.0`, `v0.2.3`)

**Jobs:**
1. **Build Binaries** - Cross-platform compilation
   - Builds for 6 platforms:
     - `linux/amd64`
     - `linux/arm64`
     - `darwin/amd64` (macOS Intel)
     - `darwin/arm64` (macOS Apple Silicon)
     - `windows/amd64`
   - Creates `.tar.gz` archives (Linux/macOS) and `.zip` (Windows)
   - Uploads artifacts for 30 days retention

2. **Build Docker Images** - Multi-architecture containers
   - Builds and pushes to Docker Hub:
     - `cloudless/coordinator:latest`
     - `cloudless/coordinator:v1.0.0`
     - `cloudless/agent:latest`
     - `cloudless/agent:v1.0.0`
   - Supports `linux/amd64` and `linux/arm64`
   - Uses semantic versioning tags

3. **Create GitHub Release**
   - Generates changelog from git commits
   - Attaches binary archives
   - Includes Docker pull instructions
   - Marks pre-releases if tag contains `-` (e.g., `v1.0.0-beta`)

**Total Duration:** ~15-25 minutes

**Required Secrets:**
- `DOCKERHUB_USERNAME` - Docker Hub username
- `DOCKERHUB_TOKEN` - Docker Hub access token

### 3. CodeQL Security Scan (`.github/workflows/codeql.yml`)

**Triggers:**
- Push to `main` branch
- Pull requests to `main` branch
- Weekly schedule (Sundays at 00:00 UTC)

**Analysis:**
- Scans Go code for security vulnerabilities
- Uses CodeQL queries: `security-extended` and `security-and-quality`
- Results uploaded to GitHub Security tab

**Duration:** ~5-10 minutes

### 4. Dependabot (`.github/dependabot.yml`)

**Configuration:**
- **Go Modules:** Weekly updates on Mondays at 09:00 UTC
  - Groups minor and patch updates
  - Maximum 10 open PRs
  - Labels: `dependencies`, `go`

- **GitHub Actions:** Weekly updates on Mondays at 09:00 UTC
  - Maximum 5 open PRs
  - Labels: `dependencies`, `github-actions`

- **Docker Base Images:** Monthly updates on the 1st at 09:00 UTC
  - Maximum 5 open PRs
  - Labels: `dependencies`, `docker`

## How to Release

### Step 1: Prepare Release

```bash
# Ensure main branch is up to date
git checkout main
git pull origin main

# Run tests locally
make test
make lint
make build
```

### Step 2: Create Release Tag

```bash
# Create and push a semantic version tag
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0
```

### Step 3: Monitor Release Workflow

1. Go to [GitHub Actions](https://github.com/osama1998H/Cloudless/actions)
2. Click on the "Release" workflow run
3. Monitor the progress of:
   - Binary builds for all platforms
   - Docker image builds and pushes
   - GitHub release creation

### Step 4: Verify Release

1. **Check GitHub Release:**
   - Visit [Releases page](https://github.com/osama1998H/Cloudless/releases)
   - Verify binary archives are attached
   - Verify changelog is generated

2. **Check Docker Hub:**
   ```bash
   docker pull cloudless/coordinator:v1.0.0
   docker pull cloudless/agent:v1.0.0
   ```

3. **Test Binaries:**
   - Download binary for your platform
   - Verify version: `./coordinator --version`

## Repository Setup

### Required Secrets

Configure these in **Settings → Secrets and variables → Actions**:

| Secret Name | Description | Required For |
|-------------|-------------|--------------|
| `DOCKERHUB_USERNAME` | Docker Hub username | Release workflow |
| `DOCKERHUB_TOKEN` | Docker Hub access token | Release workflow |
| `CODECOV_TOKEN` | Codecov upload token | CI workflow (optional) |

### Branch Protection

Recommended settings for `main` branch:

- ✅ Require pull request reviews before merging
- ✅ Require status checks to pass:
  - `Lint`
  - `Test`
  - `Build`
  - `Docker Build`
- ✅ Require branches to be up to date before merging
- ✅ Require linear history
- ✅ Include administrators

## Local Testing

### Run CI Checks Locally

```bash
# Full CI pipeline
make ci

# Individual checks
make lint        # Run golangci-lint
make test        # Run unit tests with coverage
make build       # Build all binaries
make docker      # Build Docker images
```

### Test Release Build

```bash
# Cross-compile for Linux AMD64
make build-linux-amd64

# Build Docker images
make docker

# Tag Docker images
docker tag cloudless/coordinator:latest cloudless/coordinator:v0.0.0-test
docker tag cloudless/agent:latest cloudless/agent:v0.0.0-test
```

## Troubleshooting

### CI Failures

**Lint Failures:**
- Run `make fmt` to auto-format code
- Run `make lint` locally to see all issues
- Check golangci-lint version matches CI (v1.62.2)

**Test Failures:**
- Run `make test` locally
- Check if tests pass with `go test -v ./...`
- Ensure protobuf code is regenerated: `make proto`

**Build Failures:**
- Verify Go version matches `go.mod` (1.24.0)
- Check dependencies: `make mod`
- Clear cache: `go clean -cache -modcache -testcache`

**Docker Build Failures:**
- Test locally: `make docker`
- Check Dockerfile syntax
- Verify base images are accessible

### Release Failures

**Binary Build Failures:**
- Test cross-compilation locally:
  ```bash
  GOOS=linux GOARCH=amd64 go build ./cmd/coordinator
  GOOS=darwin GOARCH=arm64 go build ./cmd/agent
  ```
- Check for platform-specific code without build tags

**Docker Push Failures:**
- Verify Docker Hub credentials are correct
- Check Docker Hub repository permissions
- Ensure repository names match: `cloudless/coordinator`, `cloudless/agent`

**GitHub Release Failures:**
- Verify tag format: `v*.*.*` (must start with 'v')
- Check repository permissions for GitHub token
- Ensure artifacts were uploaded successfully

## Maintenance

### Updating Workflows

When modifying workflows:

1. Test changes in a fork or feature branch first
2. Use `act` tool to test GitHub Actions locally:
   ```bash
   # Install act: https://github.com/nektos/act
   brew install act

   # Test CI workflow
   act push -j test
   ```
3. Monitor workflow runs closely after changes
4. Keep workflow dependencies up to date (Go version, action versions)

### Updating Dependencies

Dependabot automatically creates PRs for:
- Go module updates (weekly)
- GitHub Actions updates (weekly)
- Docker base image updates (monthly)

**Review Process:**
1. Check PR description for changes
2. Review test results
3. Check for breaking changes in release notes
4. Merge if tests pass and changes are safe

### Security Updates

- **Critical vulnerabilities:** Merge Dependabot PRs immediately
- **CodeQL alerts:** Review in Security tab, create issues, fix promptly
- **Go version updates:** Update `go.mod` and CI workflow Go version together

## Performance Optimization

### CI Speed

Current optimizations:
- ✅ Go module caching with `actions/setup-go@v5`
- ✅ Docker layer caching with `type=gha`
- ✅ Parallel job execution (lint + test)
- ✅ Artifact caching between jobs

Future optimizations:
- Consider matrix parallelization for tests
- Split integration tests into separate workflow
- Use self-hosted runners for faster builds

### Docker Build Speed

- Docker Buildx uses GitHub Actions cache
- Multi-stage builds minimize layer size
- Base images are cached between runs

## Additional Resources

- [GitHub Actions Documentation](https://docs.github.com/en/actions)
- [Docker Buildx Documentation](https://docs.docker.com/buildx/working-with-buildx/)
- [Dependabot Documentation](https://docs.github.com/en/code-security/dependabot)
- [CodeQL Documentation](https://codeql.github.com/docs/)
- [Semantic Versioning](https://semver.org/)

## Questions?

For CI/CD issues or questions:
- Open an issue: [GitHub Issues](https://github.com/osama1998H/Cloudless/issues)
- Tag with label: `ci/cd`
