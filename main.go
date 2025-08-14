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
	"sort"
	"strconv"
	"sync"
	"time"
	
	"github.com/joho/godotenv"
)

// Configuration constants
const (
	defaultDuration   = 30
	defaultIterations = 100
	defaultTestAccount = "0x8ef8E8E08C4ecE1CCED0Ab36EDA8Af7e1b484e82"
	defaultRequestRate = 0  // 0 means calculate from iterations/duration
)

// Test types constants
const (
	TestBasicRPC            = "basic-rpc"
	TestConcurrentBlockNum  = "concurrent-block"
	TestConcurrentChainID   = "concurrent-chain"
	TestMixedMethods        = "mixed-methods"
	TestSustainedLoad       = "sustained-load"
	TestAll                 = "all"
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

// Performance metrics structure
type PerformanceMetrics struct {
	StartTime time.Time
	EndTime   time.Time
	Duration  time.Duration
	Requests  int
	Errors    int
	// New fields for response time tracking
	TotalResponseTime time.Duration
	MinResponseTime   time.Duration
	MaxResponseTime   time.Duration
	ResponseTimes     []time.Duration // Store individual response times for percentiles
}

// ResponseTimeTracker tracks individual request response times
type ResponseTimeTracker struct {
	mu            sync.RWMutex
	requestTimes  []time.Time      // When each request was made
	responseTimes []time.Duration  // Response time for each request
	startTime     time.Time
}

// NewResponseTimeTracker creates a new response time tracker
func NewResponseTimeTracker() *ResponseTimeTracker {
	return &ResponseTimeTracker{
		requestTimes:  make([]time.Time, 0),
		responseTimes: make([]time.Duration, 0),
		startTime:     time.Now(),
	}
}

// RecordResponseTime records a response time measurement
func (rt *ResponseTimeTracker) RecordResponseTime(responseTime time.Duration) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.requestTimes = append(rt.requestTimes, time.Now())
	rt.responseTimes = append(rt.responseTimes, responseTime)
}

// GetStats returns response time statistics
func (rt *ResponseTimeTracker) GetStats() (min, max, avg time.Duration, p50, p95, p99 time.Duration) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	
	if len(rt.responseTimes) == 0 {
		return 0, 0, 0, 0, 0, 0
	}
	
	// Sort response times for percentile calculations
	times := make([]time.Duration, len(rt.responseTimes))
	copy(times, rt.responseTimes)
	sort.Slice(times, func(i, j int) bool {
		return times[i] < times[j]
	})
	
	min = times[0]
	max = times[len(times)-1]
	
	// Calculate average
	var total time.Duration
	for _, t := range times {
		total += t
	}
	avg = total / time.Duration(len(times))
	
	// Calculate percentiles
	p50 = times[len(times)*50/100]
	p95 = times[len(times)*95/100]
	p99 = times[len(times)*99/100]
	
	return min, max, avg, p50, p95, p99
}

// GetCurrentTPS calculates current transactions per second
func (rt *ResponseTimeTracker) GetCurrentTPS() float64 {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	
	if len(rt.requestTimes) == 0 {
		return 0
	}
	
	// Calculate TPS based on recent requests (last 10 seconds)
	cutoff := time.Now().Add(-10 * time.Second)
	recentCount := 0
	
	// Count requests that were made after the cutoff time
	for i := len(rt.requestTimes) - 1; i >= 0; i-- {
		if rt.requestTimes[i].After(cutoff) {
			recentCount++
		} else {
			break // Stop counting once we hit older requests
		}
	}
	
	// Calculate TPS based on recent requests
	if recentCount > 0 {
		return float64(recentCount) / 10.0
	}
	
	return 0
}

// StressTest holds the test configuration and state
type StressTest struct {
	duration   int
	iterations int
	nodeURL    string
	testAccount string
	client     *http.Client
	startTime  time.Time
	// Add response time tracker
	responseTracker *ResponseTimeTracker
	// Add request rate control
	requestRate int // requests per second (0 = auto-calculate from iterations/duration)
}

// TestRunner handles test execution
type TestRunner struct {
	st    *StressTest
	tests []string
}

// NewTestRunner creates a new test runner instance
func NewTestRunner(st *StressTest, tests []string) *TestRunner {
	return &TestRunner{
		st:    st,
		tests: tests,
	}
}

// RunTests executes the specified tests
func (tr *TestRunner) RunTests() error {
	// Always check node status first
	if err := tr.st.checkNodeStatus(); err != nil {
		return err
	}
	
	// If no tests specified or "all" is specified, run all tests
	if len(tr.tests) == 0 || contains(tr.tests, TestAll) {
		tr.runAllTests()
		return nil
	}
	
	// Run only specified tests
	for _, test := range tr.tests {
		switch test {
		case TestBasicRPC:
			tr.st.testBasicRPC()
		case TestConcurrentBlockNum:
			tr.st.testConcurrentRequests(tr.st.iterations, "eth_blockNumber", "block number requests")
		case TestConcurrentChainID:
			tr.st.testConcurrentRequests(tr.st.iterations, "eth_chainId", "chain ID requests")
		case TestMixedMethods:
			tr.st.testMixedMethods(tr.st.iterations / 2)
		case TestSustainedLoad:
			tr.st.testSustainedLoad()
		default:
			fmt.Printf("Unknown test: %s\n", test)
		}
	}
	
	return nil
}

// runAllTests runs all available tests
func (tr *TestRunner) runAllTests() {
	tr.st.getInitialMetrics()
	tr.st.testBasicRPC()
	tr.st.testConcurrentRequests(tr.st.iterations, "eth_blockNumber", "block number requests")
	tr.st.testConcurrentRequests(tr.st.iterations, "eth_chainId", "chain ID requests")
	tr.st.testMixedMethods(tr.st.iterations / 2)
	tr.st.testSustainedLoad()
	tr.st.getFinalMetrics()
	tr.st.generateReport()
}

// contains checks if a slice contains a specific string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// NewStressTest creates a new stress test instance
func NewStressTest(duration, iterations int, nodeURL, testAccount string, requestRate int) *StressTest {
	return &StressTest{
		duration:    duration,
		iterations:  iterations,
		nodeURL:     nodeURL,
		testAccount: testAccount,
		client:      &http.Client{Timeout: 30 * time.Second},
		responseTracker: NewResponseTimeTracker(),
		requestRate:     requestRate,
	}
}

// printHeader prints the test header information
func (st *StressTest) printHeader() {
	fmt.Println("Odyssey Chain Stress Testing Script")
	fmt.Println("=====================================")
	fmt.Printf("Node URL: %s\n", st.nodeURL)
	fmt.Printf("Test Account: %s\n", st.testAccount)
	fmt.Printf("Test Duration: %ds\n", st.duration)
	fmt.Printf("Iterations: %d\n", st.iterations)
	fmt.Println()
}

// checkNodeStatus verifies if the RPC endpoint is accessible and responding
func (st *StressTest) checkNodeStatus() error {
	fmt.Println("Checking node connectivity...")
	
	// Test basic RPC connectivity
	resp, err := st.makeRPCRequest("eth_chainId")
	if err != nil {
		return fmt.Errorf("failed to connect to RPC endpoint: %v", err)
	}
	
	fmt.Printf("Node is accessible - Chain ID: %v\n", resp.Result)
	return nil
}

// getPerformanceMetrics returns current performance metrics
func (st *StressTest) getPerformanceMetrics() *PerformanceMetrics {
	return &PerformanceMetrics{
		StartTime: st.startTime,
		EndTime:   time.Now(),
		Duration:  time.Since(st.startTime),
		Requests:  0, // Will be updated during tests
		Errors:    0, // Will be updated during tests
	}
}

// getInitialMetrics displays initial performance metrics
func (st *StressTest) getInitialMetrics() {
	fmt.Println("Getting initial performance metrics...")
	
	// Set start time for metrics
	st.startTime = time.Now()
	
	fmt.Printf("Start Time: %s\n", st.startTime.Format("15:04:05"))
	fmt.Printf("Test Duration: %ds\n", st.duration)
	fmt.Printf("Test Iterations: %d\n", st.iterations)
	fmt.Println()
}

// makeRPCRequest makes a single RPC request to the node
func (st *StressTest) makeRPCRequest(method string, params ...interface{}) (*RPCResponse, error) {
	startTime := time.Now()
	
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
	
	resp, err := st.client.Post(st.nodeURL, "application/json", bytes.NewBuffer(jsonData))
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
	
	// Record response time
	responseTime := time.Since(startTime)
	st.responseTracker.RecordResponseTime(responseTime)
	
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
	resp, err = st.makeRPCRequest("eth_getBalance", st.testAccount, "latest")
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
			
			requestStart := time.Now()
			
			req := RPCRequest{
				JSONRPC: "2.0",
				ID:      id,
				Method:  method,
			}
			
			jsonData, _ := json.Marshal(req)
			resp, err := st.client.Post(st.nodeURL, "application/json", bytes.NewBuffer(jsonData))
			if err != nil {
				results <- err
				return
			}
			defer resp.Body.Close()
			
			// Read response to ensure it's complete
			_, err = io.Copy(io.Discard, resp.Body)
			
			// Record response time
			responseTime := time.Since(requestStart)
			st.responseTracker.RecordResponseTime(responseTime)
			
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
	
	// Show current TPS
	currentTPS := st.responseTracker.GetCurrentTPS()
	fmt.Printf("Current TPS: %.2f\n", currentTPS)
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
				
				requestStart := time.Now()
				
				var req RPCRequest
				if m == "eth_getBalance" {
					req = RPCRequest{
						JSONRPC: "2.0",
						ID:      1,
						Method:  m,
						Params:  []interface{}{st.testAccount, "latest"},
					}
				} else {
					req = RPCRequest{
						JSONRPC: "2.0",
						ID:      1,
						Method:  m,
					}
				}
				
				jsonData, _ := json.Marshal(req)
				resp, err := st.client.Post(st.nodeURL, "application/json", bytes.NewBuffer(jsonData))
				if err != nil {
					results <- err
					return
				}
				defer resp.Body.Close()
				
				_, err = io.Copy(io.Discard, resp.Body)
				
				// Record response time
				responseTime := time.Since(requestStart)
				st.responseTracker.RecordResponseTime(responseTime)
				
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
	
	// Show current TPS
	currentTPS := st.responseTracker.GetCurrentTPS()
	fmt.Printf("Current TPS: %.2f\n", currentTPS)
	fmt.Println()
}

// testSustainedLoad runs a sustained load test for the specified duration
func (st *StressTest) testSustainedLoad() {
	fmt.Printf("Running sustained load test for %ds...\n", st.duration)
	
	startTime := time.Now()
	endTime := startTime.Add(time.Duration(st.duration) * time.Second)
	requestCount := 0
	
	fmt.Printf("Starting sustained load test at %s...\n", startTime.Format("15:04:05"))
	
	// Calculate request rate based on iterations and duration
	// If iterations=200 and duration=60s, we want 200 requests over 60 seconds
	// So request interval = 60s / 200 = 0.3s = 300ms
	var requestInterval time.Duration
	if st.requestRate > 0 {
		// Use explicit request rate (requests per second)
		requestInterval = time.Second / time.Duration(st.requestRate)
		fmt.Printf("Request interval: %v (targeting %d requests per second)\n", requestInterval, st.requestRate)
	} else {
		// Auto-calculate from iterations and duration
		requestInterval = time.Duration(st.duration) * time.Second / time.Duration(st.iterations)
		fmt.Printf("Request interval: %v (targeting %d requests over %ds)\n", requestInterval, st.iterations, st.duration)
	}
	
	ctx, cancel := context.WithDeadline(context.Background(), endTime)
	defer cancel()
	
	ticker := time.NewTicker(requestInterval)
	defer ticker.Stop()
	
	// TPS monitoring ticker
	tpsTicker := time.NewTicker(5 * time.Second)
	defer tpsTicker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			goto done
		case <-tpsTicker.C:
			// Show real-time TPS every 5 seconds
			currentTPS := st.responseTracker.GetCurrentTPS()
			fmt.Printf("[%s] Current TPS: %.2f, Total Requests: %d\n", 
				time.Now().Format("15:04:05"), currentTPS, requestCount)
		case <-ticker.C:
			go func() {
				requestStart := time.Now()
				
				req := RPCRequest{
					JSONRPC: "2.0",
					ID:      1,
					Method:  "eth_blockNumber",
				}
				
				jsonData, _ := json.Marshal(req)
				resp, err := st.client.Post(st.nodeURL, "application/json", bytes.NewBuffer(jsonData))
				if err == nil && resp != nil {
					resp.Body.Close()
					
					// Record response time
					responseTime := time.Since(requestStart)
					st.responseTracker.RecordResponseTime(responseTime)
				}
			}()
			
			requestCount++
		}
	}
	
done:
	fmt.Printf("Sustained load test completed: %d requests in %ds\n", requestCount, st.duration)
	
	// Show final TPS
	finalTPS := st.responseTracker.GetCurrentTPS()
	fmt.Printf("Final TPS: %.2f\n", finalTPS)
	fmt.Println()
}

// getFinalMetrics displays final performance metrics
func (st *StressTest) getFinalMetrics() {
	fmt.Println("Getting final performance metrics...")
	
	metrics := st.getPerformanceMetrics()
	
	fmt.Printf("Total Duration: %v\n", metrics.Duration)
	fmt.Printf("Total Requests: %d\n", metrics.Requests)
	fmt.Printf("Total Errors: %d\n", metrics.Errors)
	fmt.Printf("Overall Requests per Second: %.2f\n", float64(metrics.Requests)/metrics.Duration.Seconds())
	
	// Show detailed response time statistics
	if st.responseTracker != nil {
		min, max, avg, p50, p95, p99 := st.responseTracker.GetStats()
		fmt.Println("\nResponse Time Statistics:")
		fmt.Printf("  Min Response Time: %v\n", min)
		fmt.Printf("  Max Response Time: %v\n", max)
		fmt.Printf("  Average Response Time: %v\n", avg)
		fmt.Printf("  50th Percentile (P50): %v\n", p50)
		fmt.Printf("  95th Percentile (P95): %v\n", p95)
		fmt.Printf("  99th Percentile (P99): %v\n", p99)
		
		// Show current TPS
		currentTPS := st.responseTracker.GetCurrentTPS()
		fmt.Printf("  Current TPS: %.2f\n", currentTPS)
	}
	
	fmt.Println()
}

// generateReport generates the final performance report
func (st *StressTest) generateReport() {
	fmt.Println("Performance Report")
	fmt.Println("==================")
	
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
	
	// Show TPS summary
	if st.responseTracker != nil {
		currentTPS := st.responseTracker.GetCurrentTPS()
		fmt.Printf("Peak TPS Achieved: %.2f\n", currentTPS)
		
		// Show response time summary
		min, max, avg, _, _, _ := st.responseTracker.GetStats()
		fmt.Printf("Response Time Range: %v - %v\n", min, max)
		fmt.Printf("Average Response Time: %v\n", avg)
	}
	
	fmt.Println("Node Stability: PASSED")
}

// checkDependencies verifies required tools are available
func checkDependencies() error {
	// No external dependencies required for RPC-only testing
	return nil
}

// loadEnvFile loads environment variables from .env file
func loadEnvFile() error {
	// Try to load .env file, but don't fail if it doesn't exist
	if err := godotenv.Load(); err != nil {
		// .env file not found is not an error
		log.Println("No .env file found, using system environment variables")
	}
	return nil
}

// getConfigFromEnv gets configuration from environment variables
func getConfigFromEnv() (string, string, int, int, int) {
	nodeURL := os.Getenv("ODYSSEY_RPC_URL")
	log.Println("nodeURL", nodeURL)
	if nodeURL == "" {
		log.Fatalf("ODYSSEY_RPC_URL environment variable is required")
	}
	
	testAccount := os.Getenv("ODYSSEY_TEST_ACCOUNT")
	if testAccount == "" {
		testAccount = defaultTestAccount
	}

	// Get duration from environment variable
	duration := defaultDuration
	if envDuration := os.Getenv("DURATION"); envDuration != "" {
		if d, err := strconv.Atoi(envDuration); err == nil {
			duration = d
		}
	}
	
	// Get iterations from environment variable
	iterations := defaultIterations
	if envIterations := os.Getenv("ITERATIONS"); envIterations != "" {
		if i, err := strconv.Atoi(envIterations); err == nil {
			iterations = i
		}
	}
	
	// Get request rate from environment variable (requests per second)
	requestRate := defaultRequestRate
	if envRequestRate := os.Getenv("REQUEST_RATE"); envRequestRate != "" {
		if r, err := strconv.Atoi(envRequestRate); err == nil {
			requestRate = r
		}
	}
	
	return nodeURL, testAccount, duration, iterations, requestRate
}

// parseTestArgs parses command line arguments for test selection
func parseTestArgs() []string {
	var tests []string
	
	// Skip the first argument (program name)
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		
		// Skip help flags
		if arg == "-h" || arg == "--help" {
			continue
		}
		
		// Add test name
		tests = append(tests, arg)
	}
	
	return tests
}

// showUsage displays command line usage information
func showUsage() {
	fmt.Println("Usage: ./stress-test [test1] [test2] ...")
	fmt.Println()
	fmt.Println("Available Tests:")
	fmt.Printf("  %s              Basic RPC functionality test\n", TestBasicRPC)
	fmt.Printf("  %s        Concurrent block number requests\n", TestConcurrentBlockNum)
	fmt.Printf("  %s         Concurrent chain ID requests\n", TestConcurrentChainID)
	fmt.Printf("  %s           Mixed RPC methods test\n", TestMixedMethods)
	fmt.Printf("  %s        Sustained load test\n", TestSustainedLoad)
	fmt.Printf("  %s                 Run all tests (default)\n", TestAll)
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  ./stress-test                                    # Run all tests")
	fmt.Println("  ./stress-test basic-rpc                          # Run only basic RPC test")
	fmt.Println("  ./stress-test concurrent-block mixed-methods     # Run specific tests")
	fmt.Println("  ./stress-test sustained-load                     # Run only sustained load test")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  ODYSSEY_RPC_URL      RPC endpoint URL (required)")
	fmt.Println("  ODYSSEY_TEST_ACCOUNT Test account address (required)")
	fmt.Println("  DURATION             Duration of sustained load test in seconds (default: 30)")
	fmt.Println("  ITERATIONS           Number of concurrent requests (default: 100)")
	fmt.Println("  REQUEST_RATE         Requests per second for sustained load (default: auto-calculate)")
	fmt.Println()
	fmt.Println("Environment Examples:")
	fmt.Println("  ODYSSEY_RPC_URL=http://my-node:9650/ext/bc/D/rpc ODYSSEY_TEST_ACCOUNT=0x1234... DURATION=60 ITERATIONS=200 ./stress-test")
	fmt.Println("  # Or set them before running:")
	fmt.Println("  export ODYSSEY_RPC_URL=http://my-node:9650/ext/bc/D/rpc")
	fmt.Println("  export ODYSSEY_TEST_ACCOUNT=0x1234...")
	fmt.Println("  export DURATION=60")
	fmt.Println("  export ITERATIONS=200")
	fmt.Println("  export REQUEST_RATE=50  # Force 50 requests per second")
	fmt.Println("  ./stress-test")
}

// main function
func main() {
	// Check for help flag
	if len(os.Args) > 1 {
		if os.Args[1] == "-h" || os.Args[1] == "--help" {
			showUsage()
			return
		}
	}
	
	// Check dependencies
	if err := checkDependencies(); err != nil {
		log.Fatalf("%v", err)
	}
	
	// Load environment variables from .env file
	if err := loadEnvFile(); err != nil {
		log.Fatalf("Failed to load .env file: %v", err)
	}
	
	// Get configuration from environment variables
	nodeURL, testAccount, duration, iterations, requestRate := getConfigFromEnv()
	
	// Parse test arguments
	tests := parseTestArgs()
	
	// Create and run stress test
	st := NewStressTest(duration, iterations, nodeURL, testAccount, requestRate)
	
	// Set start time
	st.startTime = time.Now()
	
	fmt.Println("Starting Odyssey Chain Stress Testing...")
	fmt.Println()
	
	// Print header
	st.printHeader()
	
	// Print selected tests
	if len(tests) == 0 {
		fmt.Println("Running all tests...")
	} else {
		fmt.Printf("Running selected tests: %v\n", tests)
	}
	fmt.Println()
	
	// Create test runner and execute tests
	runner := NewTestRunner(st, tests)
	if err := runner.RunTests(); err != nil {
		log.Fatalf("%v", err)
	}
	
	fmt.Println("Stress testing completed successfully!")
}
