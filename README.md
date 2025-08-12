# Dione Node Stress Test

This is a Go implementation of the Dione node stress testing script, providing the same functionality as the original shell script but with improved performance, better error handling, and cross-platform compatibility.

## Features

- **Node Health Check**: Verifies Docker container status and health
- **Basic RPC Testing**: Tests fundamental RPC methods (chain ID, block number, balance)
- **Concurrent Request Testing**: Tests multiple simultaneous RPC requests
- **Mixed Method Testing**: Tests various RPC methods concurrently
- **Sustained Load Testing**: Runs continuous requests for specified duration
- **Performance Metrics**: Monitors CPU, memory, and network usage
- **Comprehensive Reporting**: Generates detailed performance reports

## Prerequisites

- Go 1.21 or later
- Docker (for container management)
- A running Dione/OdysseyGo node

## Installation

1. Clone or download the source code
2. Navigate to the project directory
3. Build the binary:

```bash
go build -o stress-test main.go
```

## Prerequisites Setup

Before running the stress test, you need to set up the Odyssey node manually. Follow these steps:

### 1. Clone the Odyssey Installer Repository

```bash
# Clone the installer repository
git clone https://github.com/DioneProtocol/odysseygo-installer.git
cd odysseygo-installer/docker
```

### 2. Set Up and Run Odyssey Node

```bash
# Create required directories
mkdir -p data/.odysseygo data/db logs

# Create environment configuration
cat > .env << EOF
NETWORK=mainnet
STATE_SYNC=on
ARCHIVAL_MODE=false
RPC_ACCESS=public
EOF

# Start the node
docker-compose up -d
```

### 3. Wait for Node Sync

The node needs time to sync:
- **First run**: Several hours (bootstrap download + sync)
- **Subsequent runs**: Minutes to hours depending on sync status
- **Monitor progress**: `docker-compose logs -f`

**Note**: The first sync may take several hours. Be patient!

## Usage

### Basic Usage

```bash
# Use default values (30s duration, 100 iterations)
./stress-test

# Custom duration (60 seconds)
./stress-test 60

# Custom duration and iterations (60s, 200 iterations)
./stress-test 60 200

# Show help
./stress-test --help
```

### Command Line Parameters

- **duration**: Duration of sustained load test in seconds (default: 30)
- **iterations**: Number of concurrent requests (default: 100)

### Examples

```bash
# Quick test
./stress-test

# Extended stress test
./stress-test 120 500

# High-load test
./stress-test 300 1000
```

## Configuration

The stress test uses the following default configuration (defined in `main.go`):

```go
const (
    defaultDuration   = 30
    defaultIterations = 100
    nodeURL          = "http://localhost:9650/ext/bc/L1m631VHS1yuYkicaNRQTzzbE71dG942sgF3sCnHFgCTzNmsD/rpc"
    containerName    = "docker-odysseygo-1"
    testAccount      = "0x8ef8E8E08C4ecE1CCED0Ab36EDA8Af7e1b484e82"
)
```

### Customizing Configuration

You can customize the stress test by modifying the constants in `main.go`:

- **`nodeURL`**: Change this if your Odyssey node runs on a different host/port
- **`containerName`**: Update if you're using a different Docker container name
- **`testAccount`**: Change to test with a different account address

### Network Configuration

When setting up your Odyssey node, you can configure it via the `.env` file:

```bash
# Network Configuration
NETWORK=mainnet          # Use 'testnet' for testnet
STATE_SYNC=on            # Fast sync for validators
ARCHIVAL_MODE=false      # Disable for validators (enable for archive nodes)
RPC_ACCESS=public        # Allow external RPC access
```

To switch to testnet, edit the `.env` file and change `NETWORK=mainnet` to `NETWORK=testnet`.

### Environment Variables

You can also customize your Odyssey node setup using these environment variables in your `.env` file:

```bash
# Network and Sync
NETWORK=mainnet                    # mainnet or testnet
STATE_SYNC=on                      # on or off (recommended: on for validators)
ARCHIVAL_MODE=false                # true or false (true for archive nodes)

# RPC Access
RPC_ACCESS=public                  # public or private
RPC_HOST_PORT=9650                 # RPC port (default: 9650)
P2P_HOST_PORT=9651                 # P2P port (default: 9651)

# Performance
LOG_LEVEL_NODE=info                # Node log level
LOG_LEVEL_DCHAIN=info              # D-Chain log level
INDEX_ENABLED=true                 # Enable indexer
ADMIN_API=false                    # Enable admin API
ETH_DEBUG_RPC=true                 # Enable debug RPC
```

To modify these settings, edit the constants in `main.go` and rebuild.

## Setup Process

### 1. Manual Setup (Required)

```bash
# Clone the installer repository
cd ..
git clone https://github.com/DioneProtocol/odysseygo-installer.git
cd odysseygo-installer/docker

# Create directories
mkdir -p data/.odysseygo data/db logs

# Create configuration
cat > .env << EOF
NETWORK=mainnet
STATE_SYNC=on
ARCHIVAL_MODE=false
RPC_ACCESS=public
EOF

# Start the node
docker-compose up -d
```

### 2. Wait for Sync

The node needs time to sync:
- **First run**: Several hours (bootstrap download + sync)
- **Subsequent runs**: Minutes to hours depending on sync status
- **Monitor progress**: `docker-compose logs -f`

### 3. Verify Node is Ready

Before running the stress test, ensure your node is fully synced and responding:

```bash
# Check if node is running
docker ps | grep odysseygo

# Check node logs
docker-compose logs -f

# Test RPC connectivity (should return a block number)
curl -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
  http://localhost:9650/ext/bc/L1m631VHS1yuYkicaNRQTzzbE71dG942sgF3sCnHFgCTzNmsD/rpc
```

**Important**: Only run the stress test when your node is fully synced and healthy!

## Test Phases
The stress test runs through several phases:


1. **Node Status Check**: Verifies Docker container is running and healthy
2. **Initial Metrics**: Captures baseline CPU, memory, and network usage
3. **Basic RPC Test**: Tests fundamental RPC functionality
4. **Concurrent Requests**: Tests multiple simultaneous requests
5. **Mixed Methods**: Tests various RPC methods concurrently
6. **Sustained Load**: Runs continuous requests for specified duration
7. **Final Metrics**: Captures final resource usage
8. **Performance Report**: Generates comprehensive test summary

## Output

The stress test provides colorful, emoji-enhanced output similar to the shell script:

- Test initialization and progress
- Metrics and monitoring
- RPC functionality testing
- Concurrent request testing
- Mixed method testing
- Sustained load testing
- Performance reporting
- Success indicators
- Error indicators
- Warning indicators

## Error Handling

The Go version includes comprehensive error handling:

- Graceful degradation when Docker stats are unavailable
- Detailed error reporting for RPC failures
- Proper cleanup of resources and goroutines
- Context cancellation for sustained load tests

## Performance Improvements

Compared to the shell script version:

- **True Concurrency**: Uses Go goroutines instead of background processes
- **Better Resource Management**: Proper HTTP client reuse and connection pooling
- **Accurate Timing**: Precise timing measurements using Go's time package
- **Memory Efficiency**: Streams responses and discards data when not needed
- **Cross-Platform**: Works on Windows, macOS, and Linux without modification

## Building for Different Platforms

```bash
# Build for current platform
go build -o stress-test main.go

# Build for Linux
GOOS=linux GOARCH=amd64 go build -o stress-test-linux main.go

# Build for Windows
GOOS=windows GOARCH=amd64 go build -o stress-test.exe main.go

# Build for macOS
GOOS=darwin GOARCH=amd64 go build -o stress-test-darwin main.go
```

## Troubleshooting

### Common Issues

1. **Docker not running**: Ensure Docker daemon is started
2. **Container not found**: Verify the container name matches your setup
3. **RPC connection failed**: Check if the node is accessible at the configured URL
4. **Permission denied**: Ensure the binary has execute permissions
5. **Node not syncing**: Check logs with `docker-compose logs -f` and ensure sufficient disk space
6. **Port conflicts**: Ensure ports 9650 and 9651 are not used by other services
7. **Bootstrap download fails**: Check internet connection and firewall settings

### Setup-Specific Issues

1. **Installer repository not found**: Clone it manually with `git clone https://github.com/DioneProtocol/odysseygo-installer.git`
2. **Node won't start**: Check Docker logs and ensure all required directories exist
3. **Sync stuck**: The first sync can take hours - be patient and monitor logs
4. **Wrong network**: Edit the `.env` file to switch between mainnet and testnet

### Debug Mode

To add debug logging, modify the Go code to include more verbose output or add logging statements.

## Comparison with Shell Script

| Feature | Shell Script | Go Version |
|---------|--------------|------------|
| Concurrency | Background processes | True goroutines |
| Error Handling | Basic | Comprehensive |
| Cross-Platform | Linux/macOS only | All platforms |
| Performance | Good | Excellent |
| Resource Usage | Higher | Lower |
| Maintenance | Shell-specific | Standard Go |

## Contributing

To extend the stress test:

1. Add new test methods to the `StressTest` struct
2. Implement additional RPC method testing
3. Add more sophisticated metrics collection
4. Enhance the reporting system

## License

This project follows the same license as the original shell script.

## Support

For issues or questions:
1. Check the troubleshooting section
2. Verify your Docker and Go setup
3. Ensure your Dione node is running and accessible
