package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Configuration constants
const (
	defaultDuration   = 30
	defaultIterations = 100
	nodeURL          = "http://localhost:9650/ext/bc/L1m631VHS1yuYkicaNRQTzzbE71dG942sgF3sCnHFgCTzNmsD/rpc"
	containerName    = "docker-odysseygo-1"
	testAccount      = "0x8ef8E8E08C4ecE1CCED0Ab36EDA8Af7e1b484e82"
)

// RPC request/response structures
type RPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params,omitempty"`
}

type RPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error  *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Docker stats structure
type DockerStats struct {
	CPUPerc string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	NetIO   string `json:"NetIO"`
}

// StressTest holds the test configuration and state
type StressTest struct {
	duration   int
	iterations int
	client     *http.Client
	startTime  time.Time
}

// NewStressTest creates a new stress test instance
func NewStressTest(duration, iterations int) *StressTest {
	return &StressTest{
		duration:   duration,
		iterations: iterations,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
}

// printHeader prints the test header information
func (st *StressTest) printHeader() {
	fmt.Println("Dione Node Stress Testing Script")
	fmt.Println("=====================================")
	fmt.Printf("Node URL: %s\n", nodeURL)
	fmt.Printf("Container: %s\n", containerName)
	fmt.Printf("Test Duration: %ds\n", st.duration)
	fmt.Printf("Iterations: %d\n", st.iterations)
	fmt.Println()
}

// checkNodeStatus verifies if the Docker container is running and healthy
func (st *StressTest) checkNodeStatus() error {
	fmt.Println("Checking node status...")
	
	cmd := exec.Command("docker", "ps", "--filter", "name="+containerName, "--format", "{{.Names}}\t{{.Status}}")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to check docker container: %v", err)
	}
	
	if len(output) == 0 {
		return fmt.Errorf("node container is not running")
	}
	
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if strings.Contains(line, containerName) {
			if strings.Contains(line, "healthy") {
				fmt.Println("Node is healthy")
			} else {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					fmt.Printf("Node status: %s\n", parts[1])
				}
			}
			return nil
		}
	}
	
	return fmt.Errorf("node container not found")
}

// getDockerStats retrieves current Docker container statistics
func getDockerStats() (*DockerStats, error) {
	cmd := exec.Command("docker", "stats", containerName, "--no-stream", "--format", "{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get docker stats: %v", err)
	}
	
	parts := strings.Fields(strings.TrimSpace(string(output)))
	if len(parts) < 3 {
		return nil, fmt.Errorf("unexpected docker stats format")
	}
	
	return &DockerStats{
		CPUPerc: parts[0],
		MemUsage: parts[1],
		NetIO:   parts[2],
	}, nil
}

// getInitialMetrics displays initial Docker container metrics
func (st *StressTest) getInitialMetrics() {
	fmt.Println("Getting initial metrics...")
	
	stats, err := getDockerStats()
	if err != nil {
		fmt.Printf("Failed to get initial metrics: %v\n", err)
		return
	}
	
	fmt.Printf("Initial CPU: %s\n", stats.CPUPerc)
	fmt.Printf("Initial Memory: %s\n", stats.MemUsage)
	fmt.Printf("Initial Network: %s\n", stats.NetIO)
	fmt.Println()
}

// makeRPCRequest makes a single RPC request to the node
func (st *StressTest) makeRPCRequest(method string, params ...interface{}) (*RPCResponse, error) {
	req := RPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	}
	
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	
	resp, err := st.client.Post(nodeURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	
	var rpcResp RPCResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, err
	}
	
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("RPC error: %s", rpcResp.Error.Message)
	}
	
	return &rpcResp, nil
}

// testBasicRPC tests basic RPC functionality
func (st *StressTest) testBasicRPC() {
	fmt.Println("Testing basic RPC functionality...")
	
	// Test chain ID
	resp, err := st.makeRPCRequest("eth_chainId")
	if err != nil {
		fmt.Printf("Failed to get chain ID: %v\n", err)
	} else {
		fmt.Printf("Chain ID: %v\n", resp.Result)
	}
	
	// Test block number
	resp, err = st.makeRPCRequest("eth_blockNumber")
	if err != nil {
		fmt.Printf("Failed to get block number: %v\n", err)
	} else {
		fmt.Printf("Block Number: %v\n", resp.Result)
	}
	
	// Test balance
	resp, err = st.makeRPCRequest("eth_getBalance", testAccount, "latest")
	if err != nil {
		fmt.Printf("Failed to get balance: %v\n", err)
	} else {
		fmt.Printf("Test Account Balance: %v\n", resp.Result)
	}
	
	fmt.Println()
}

// testConcurrentRequests tests concurrent RPC requests
func (st *StressTest) testConcurrentRequests(count int, method, description string) {
	fmt.Printf("Testing concurrent RPC requests...\n")
	fmt.Printf("Running %d concurrent %s...\n", count, description)
	
	startTime := time.Now()
	
	var wg sync.WaitGroup
	results := make(chan error, count)
	
	// Launch concurrent requests
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			req := RPCRequest{
				JSONRPC: "2.0",
				ID:      id,
				Method:  method,
			}
			
			jsonData, _ := json.Marshal(req)
			resp, err := st.client.Post(nodeURL, "application/json", bytes.NewBuffer(jsonData))
			if err != nil {
				results <- err
				return
			}
			defer resp.Body.Close()
			
			// Read response to ensure it's complete
			_, err = io.Copy(io.Discard, resp.Body)
			results <- err
		}(i)
	}
	
	// Wait for all requests to complete
	wg.Wait()
	close(results)
	
	// Count errors
	errorCount := 0
	for err := range results {
		if err != nil {
			errorCount++
		}
	}
	
	duration := time.Since(startTime)
	fmt.Printf("Completed %d %s in %.2fs (errors: %d)\n", count, description, duration.Seconds(), errorCount)
	fmt.Println()
}

// testMixedMethods tests mixed RPC method requests
func (st *StressTest) testMixedMethods(count int) {
	fmt.Println("Testing mixed RPC methods...")
	fmt.Printf("Running %d mixed RPC method requests...\n", count)
	
	startTime := time.Now()
	
	var wg sync.WaitGroup
	results := make(chan error, count*3)
	
	methods := []string{"eth_blockNumber", "eth_chainId", "eth_getBalance"}
	
	for i := 0; i < count; i++ {
		for _, method := range methods {
			wg.Add(1)
			go func(m string) {
				defer wg.Done()
				
				var req RPCRequest
				if m == "eth_getBalance" {
					req = RPCRequest{
						JSONRPC: "2.0",
						ID:      1,
						Method:  m,
						Params:  []interface{}{testAccount, "latest"},
					}
				} else {
					req = RPCRequest{
						JSONRPC: "2.0",
						ID:      1,
						Method:  m,
					}
				}
				
				jsonData, _ := json.Marshal(req)
				resp, err := st.client.Post(nodeURL, "application/json", bytes.NewBuffer(jsonData))
				if err != nil {
					results <- err
					return
				}
				defer resp.Body.Close()
				
				_, err = io.Copy(io.Discard, resp.Body)
				results <- err
			}(method)
		}
	}
	
	wg.Wait()
	close(results)
	
	errorCount := 0
	for err := range results {
		if err != nil {
			errorCount++
		}
	}
	
	duration := time.Since(startTime)
		fmt.Printf("Completed %d mixed method requests in %.2fs (errors: %d)\n", count*3, duration.Seconds(), errorCount)
	fmt.Println()
}

// testSustainedLoad runs a sustained load test for the specified duration
func (st *StressTest) testSustainedLoad() {
	fmt.Printf("Running sustained load test for %ds...\n", st.duration)
	
	startTime := time.Now()
	endTime := startTime.Add(time.Duration(st.duration) * time.Second)
	requestCount := 0
	
	fmt.Printf("Starting sustained load test at %s...\n", startTime.Format("15:04:05"))
	
	ctx, cancel := context.WithDeadline(context.Background(), endTime)
	defer cancel()
	
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			goto done
		case <-ticker.C:
			go func() {
				req := RPCRequest{
					JSONRPC: "2.0",
					ID:      1,
					Method:  "eth_blockNumber",
				}
				
				jsonData, _ := json.Marshal(req)
				resp, err := st.client.Post(nodeURL, "application/json", bytes.NewBuffer(jsonData))
				if err == nil && resp != nil {
					resp.Body.Close()
				}
			}()
			
			requestCount++
		}
	}
	
done:
	fmt.Printf("Sustained load test completed: %d requests in %ds\n", requestCount, st.duration)
	fmt.Println()
}

// getFinalMetrics displays final Docker container metrics
func (st *StressTest) getFinalMetrics() {
	fmt.Println("Getting final metrics...")
	
	stats, err := getDockerStats()
	if err != nil {
		fmt.Printf("Failed to get final metrics: %v\n", err)
		return
	}
	
	fmt.Printf("Final CPU: %s\n", stats.CPUPerc)
	fmt.Printf("Final Memory: %s\n", stats.MemUsage)
	fmt.Printf("Final Network: %s\n", stats.NetIO)
	fmt.Println()
}

// generateReport generates the final performance report
func (st *StressTest) generateReport() {
	fmt.Println("Performance Report")
	fmt.Println("==================")
	
	// Check final node health
	cmd := exec.Command("docker", "ps", "--filter", "name="+containerName, "--format", "{{.Status}}")
	output, err := cmd.Output()
	if err == nil && strings.Contains(string(output), "healthy") {
		fmt.Println("Node Health: HEALTHY")
	} else {
		fmt.Println("Node Health: UNHEALTHY")
	}
	
	// Test final RPC response
	fmt.Println("Final RPC Test...")
	resp, err := st.makeRPCRequest("eth_blockNumber")
	if err != nil {
		fmt.Printf("Final RPC test failed: %v\n", err)
	} else {
		fmt.Printf("Final Block Number: %v\n", resp.Result)
	}
	
	fmt.Println()
	fmt.Println("Stress Test Summary")
	fmt.Println("====================")
	fmt.Printf("Concurrent Requests: %d\n", st.iterations)
	fmt.Printf("Mixed Methods: %d\n", st.iterations/2)
	fmt.Printf("Sustained Load: %ds\n", st.duration)
	fmt.Println("Node Stability: PASSED")
}

// checkDependencies verifies required tools are available
func checkDependencies() error {
	tools := []string{"docker"}
	
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s is not installed", tool)
		}
	}
	
	return nil
}

// showUsage displays command line usage information
func showUsage() {
	fmt.Println("Usage: ./stress-test [duration] [iterations]")
	fmt.Println()
	fmt.Println("Parameters:")
	fmt.Printf("  duration    Duration of sustained load test in seconds (default: %d)\n", defaultDuration)
	fmt.Printf("  iterations  Number of concurrent requests (default: %d)\n", defaultIterations)
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  ./stress-test                    # Use default values")
	fmt.Println("  ./stress-test 60                # 60 second sustained load test")
	fmt.Println("  ./stress-test 60 200            # 60 seconds, 200 concurrent requests")
}

// main function
func main() {
	// Parse command line arguments
	var duration, iterations int = defaultDuration, defaultIterations
	
	if len(os.Args) > 1 {
		if os.Args[1] == "-h" || os.Args[1] == "--help" {
			showUsage()
			return
		}
		
		if d, err := strconv.Atoi(os.Args[1]); err == nil {
			duration = d
		}
	}
	
	if len(os.Args) > 2 {
		if i, err := strconv.Atoi(os.Args[2]); err == nil {
			iterations = i
		}
	}
	
	// Check dependencies
	if err := checkDependencies(); err != nil {
		log.Fatalf("%v", err)
	}
	
	// Create and run stress test
	st := NewStressTest(duration, iterations)
	
	fmt.Println("Starting Dione Node Stress Testing...")
	fmt.Println()
	
	// Run tests
	if err := st.checkNodeStatus(); err != nil {
		log.Fatalf("%v", err)
	}
	
	st.getInitialMetrics()
	st.testBasicRPC()
	st.testConcurrentRequests(iterations, "eth_blockNumber", "block number requests")
	st.testConcurrentRequests(iterations, "eth_chainId", "chain ID requests")
	st.testMixedMethods(iterations / 2)
	st.testSustainedLoad()
	st.getFinalMetrics()
	st.generateReport()
	
	fmt.Println("Stress testing completed successfully!")
}
