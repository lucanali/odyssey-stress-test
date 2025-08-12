# Dione Node Stress Test

This is a Go implementation of the Dione node stress testing script, providing the same functionality as the original shell script but with improved performance, better error handling, and cross-platform compatibility.

## Features

- **Basic RPC Testing**: Tests fundamental RPC methods (chain ID, block number, balance)
- **Concurrent Request Testing**: Tests multiple simultaneous RPC requests
- **Mixed Method Testing**: Tests various RPC methods concurrently
- **Sustained Load Testing**: Runs continuous requests for specified duration
- **Performance Metrics**: Monitors CPU, memory, and network usage
- **Comprehensive Reporting**: Generates detailed performance reports

## Prerequisites

- Go 1.21 or later
- Network access to the RPC endpoint

## Installation

1. Clone or download the source code
2. Navigate to the project directory
3. Build the binary:

```bash
go build -o stress-test main.go
```

## Configuration

The stress test is configured via environment variables. You can either use a remote Odyssey node or run a local one.

### Environment Variables

```bash
# Set your Odyssey node RPC endpoint
export ODYSSEY_RPC_URL="http://your-node:9650/ext/bc/D/rpc"

# Set a test account address for balance checks
export ODYSSEY_TEST_ACCOUNT="0x1234567890abcdef..."

# Optional: Set custom test parameters
export DURATION=60        # 60 seconds sustained load test
export ITERATIONS=200     # 200 concurrent requests

# Run the stress test
./stress-test
```

### Running a Local Node

To run a local Odyssey node for testing, you can use the official installer repository https://github.com/DioneProtocol/odysseygo-installer.git

### Required Configuration

The stress test requires these environment variables to be set:

- **ODYSSEY_RPC_URL**: Your Odyssey node RPC endpoint (required)
- **ODYSSEY_TEST_ACCOUNT**: Test account address for balance checks (required)
- **DURATION**: Duration of sustained load test in seconds (optional, default: 30)
- **ITERATIONS**: Number of concurrent requests (optional, default: 100)

### RPC Endpoint Requirements

Your Odyssey node must:
- Be running and accessible via HTTP/HTTPS
- Have RPC endpoints enabled
- Support the following Ethereum-compatible RPC methods:
  - `eth_chainId`
  - `eth_blockNumber`
  - `eth_getBalance`

### Verify Node Connectivity

Before running the stress test, ensure your node is responding:

```bash
# Test RPC connectivity (should return a block number)
curl -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
  $ODYSSEY_RPC_URL
```

**Important**: Only run the stress test when your node is fully synced and healthy!

## Usage

### Basic Usage

```bash
# Use default values (30s duration, 100 iterations)
./stress-test

# Custom duration and iterations via environment variables
export DURATION=60
export ITERATIONS=200
./stress-test

# Or set them inline
DURATION=60 ITERATIONS=200 ./stress-test

# Show help
./stress-test --help
```

### Test Selection

You can specify which tests to run by providing test names as command line arguments:

```bash
# Run all tests (default behavior)
./stress-test

# Run only specific tests
./stress-test basic-rpc
./stress-test concurrent-block
./stress-test mixed-methods
./stress-test sustained-load

# Run multiple specific tests
./stress-test basic-rpc concurrent-block
./stress-test concurrent-chain mixed-methods

# Run all tests explicitly
./stress-test all
```

#### Available Tests

- **`basic-rpc`**: Tests fundamental RPC methods (chain ID, block number, balance)
- **`concurrent-block`**: Tests concurrent block number requests
- **`concurrent-chain`**: Tests concurrent chain ID requests  
- **`mixed-methods`**: Tests various RPC methods concurrently
- **`sustained-load`**: Runs continuous requests for specified duration
- **`all`**: Runs all tests (default when no arguments provided)

## Output

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

## Troubleshooting

### Common Issues

1. **RPC connection failed**: Check if the node is accessible at the configured URL
2. **Permission denied**: Ensure the binary has execute permissions
3. **Node not responding**: Verify the node is running and synced
4. **Port conflicts**: Ensure the RPC port is not blocked by firewall
5. **Network timeout**: Check network connectivity to the RPC endpoint

### RPC-Specific Issues

1. **Invalid RPC URL**: Ensure the URL format is correct (e.g., `http://host:port/ext/bc/D/rpc`)
2. **Authentication required**: Some nodes require API keys or authentication
3. **Rate limiting**: The node may have request rate limits
4. **Method not supported**: Ensure the node supports the required RPC methods
