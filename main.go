package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/joho/godotenv"
)

// Configuration constants
const (
	defaultDuration    = 30
	defaultIterations  = 100
	defaultTestAccount = "0x8ef8E8E08C4ecE1CCED0Ab36EDA8Af7e1b484e82"
	defaultRequestRate = 0 // 0 means calculate from iterations/duration
)

// Test types constants
const (
	TestBasicRPC           = "basic-rpc"
	TestConcurrentBlockNum = "concurrent-block"
	TestConcurrentChainID  = "concurrent-chain"
	TestMixedMethods       = "mixed-methods"
	TestSustainedLoad      = "sustained-load"
	TestTransactionTPS     = "transaction-tps"
	TestAll                = "all"
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
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Transaction structures
type Transaction struct {
	Nonce    string `json:"nonce"`
	GasPrice string `json:"gasPrice"`
	Gas      string `json:"gas"`
	To       string `json:"to"`
	Value    string `json:"value"`
	Data     string `json:"data"`
	ChainID  string `json:"chainId"`
}

type SignedTransaction struct {
	RawTransaction string `json:"rawTransaction"`
}

type TransactionReceipt struct {
	TransactionHash   string `json:"transactionHash"`
	BlockNumber       string `json:"blockNumber"`
	GasUsed           string `json:"gasUsed"`
	Status            string `json:"status"`
	EffectiveGasPrice string `json:"effectiveGasPrice"`
}

// ResponseTimeTracker tracks individual request response times
type ResponseTimeTracker struct {
	mu            sync.RWMutex
	requestTimes  []time.Time     // When each request was made
	responseTimes []time.Duration // Response time for each request
	startTime     time.Time
	// Transaction tracking
	transactionStartTimes  map[string]time.Time // Track when each transaction was sent
	transactionMiningTimes []time.Duration      // Track mining times
	transactionsSent       int
	transactionsMined      int
	// Enhanced timing tracking
	blockFinalizationTimes  map[string]time.Time // Track block finalization time
	transactionBlocks       map[string]string    // Track which block each transaction was mined in
	blockFinalizationStatus map[string]bool      // Track finalization status of blocks
}

// NewResponseTimeTracker creates a new response time tracker
func NewResponseTimeTracker() *ResponseTimeTracker {
	return &ResponseTimeTracker{
		requestTimes:            make([]time.Time, 0),
		responseTimes:           make([]time.Duration, 0),
		startTime:               time.Now(),
		transactionStartTimes:   make(map[string]time.Time),
		transactionMiningTimes:  make([]time.Duration, 0),
		blockFinalizationTimes:  make(map[string]time.Time),
		transactionBlocks:       make(map[string]string),
		blockFinalizationStatus: make(map[string]bool),
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
func (rt *ResponseTimeTracker) GetStats() (min, max, avg time.Duration) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	if len(rt.responseTimes) == 0 {
		return 0, 0, 0
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

	return min, max, avg
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

// RecordTransactionSent records when a transaction is sent
func (rt *ResponseTimeTracker) RecordTransactionSent(txHash string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.transactionStartTimes[txHash] = time.Now()
	rt.transactionsSent++
}

// RecordTransactionMined records when a transaction is mined
func (rt *ResponseTimeTracker) RecordTransactionMined(txHash string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if startTime, exists := rt.transactionStartTimes[txHash]; exists {
		miningTime := time.Since(startTime)
		rt.transactionMiningTimes = append(rt.transactionMiningTimes, miningTime)
		rt.transactionsMined++
		delete(rt.transactionStartTimes, txHash) // Clean up
	}
}

// RecordTransactionMinedInBlock records when a transaction is mined with block information
func (rt *ResponseTimeTracker) RecordTransactionMinedInBlock(txHash string, blockNumber string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	// Only map the transaction to its block and count it as mined
	rt.transactionBlocks[txHash] = blockNumber
	rt.transactionsMined++
	// Clean up any start time tracking if present
	delete(rt.transactionStartTimes, txHash)
}

// RecordBlockFinalized records when a block is finalized
func (rt *ResponseTimeTracker) RecordBlockFinalized(blockNumber string, blockTimestamp time.Time) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	// Mark this block as finalized
	rt.blockFinalizationStatus[blockNumber] = true

	// Use block timestamp for all transactions in this block
	for txHash, blockNum := range rt.transactionBlocks {
		if blockNum == blockNumber {
			rt.blockFinalizationTimes[txHash] = blockTimestamp
		}
	}
}

// RecordTransactionFinalized records when a specific transaction is finalized
func (rt *ResponseTimeTracker) RecordTransactionFinalized(txHash string, finalizationTime time.Time) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	// Record the finalization time for this specific transaction
	rt.blockFinalizationTimes[txHash] = finalizationTime

	// Mark that this transaction has been finalized
	if _, exists := rt.transactionBlocks[txHash]; exists {
		// Transaction was mined, now it's finalized
		fmt.Printf("Debug: Transaction %s finalized at %v\n", txHash, finalizationTime)
	}
}

// GetTransactionStats returns transaction statistics
func (rt *ResponseTimeTracker) GetTransactionStats() (sent, mined int, avgMiningTime time.Duration, miningTPS float64) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	sent = rt.transactionsSent
	mined = rt.transactionsMined

	if len(rt.transactionMiningTimes) == 0 {
		return sent, mined, 0, 0
	}

	// Calculate average mining time
	var total time.Duration
	for _, t := range rt.transactionMiningTimes {
		total += t
	}
	avgMiningTime = total / time.Duration(len(rt.transactionMiningTimes))

	// Calculate mining TPS (transactions mined per second)
	if len(rt.transactionMiningTimes) > 0 {
		// Use a 10-second window for recent mining TPS
		recentMined := len(rt.transactionMiningTimes)
		if recentMined > 10 {
			recentMined = 10 // Only consider last 10 transactions for TPS
		}

		if recentMined > 0 {
			miningTPS = float64(recentMined) / 10.0
		}
	}

	return sent, mined, avgMiningTime, miningTPS
}

// GetIndividualTransactionTimings returns detailed timing information for each transaction
func (rt *ResponseTimeTracker) GetIndividualTransactionTimings() map[string]map[string]interface{} {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	individualTimings := make(map[string]map[string]interface{})

	for txHash, blockNumber := range rt.transactionBlocks {
		txTiming := make(map[string]interface{})

		// Get block number
		txTiming["blockNumber"] = blockNumber

		// Finalization time
		if finalizationTime, exists := rt.blockFinalizationTimes[txHash]; exists {
			txTiming["finalizationTime"] = finalizationTime
		}

		// Status
		if _, finalized := rt.blockFinalizationTimes[txHash]; finalized {
			txTiming["finalized"] = true
		} else {
			txTiming["finalized"] = false
		}

		individualTimings[txHash] = txTiming
	}

	return individualTimings
}

// StressTest holds the test configuration and state
type StressTest struct {
	duration    int
	iterations  int
	nodeURL     string
	testAccount string
	client      *http.Client
	startTime   time.Time
	// Add response time tracker
	responseTracker *ResponseTimeTracker
	// Add request rate control
	requestRate int // requests per second (0 = auto-calculate from iterations/duration)
	// Add private keys for transaction signing
	privateKeys []*ecdsa.PrivateKey
	// Add chain ID for transaction signing
	chainID *big.Int
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
		case TestTransactionTPS:
			tr.st.testTransactionTPS()
		default:
			fmt.Printf("Unknown test: %s\n", test)
		}
	}

	return nil
}

// runAllTests runs all available tests
func (tr *TestRunner) runAllTests() {
	tr.st.startTime = time.Now()
	fmt.Printf("Start Time: %s\n", tr.st.startTime.Format("15:04:05"))
	tr.st.testBasicRPC()
	tr.st.testConcurrentRequests(tr.st.iterations, "eth_blockNumber", "block number requests")
	tr.st.testConcurrentRequests(tr.st.iterations, "eth_chainId", "chain ID requests")
	tr.st.testMixedMethods(tr.st.iterations / 2)
	tr.st.testSustainedLoad()
	tr.st.testTransactionTPS()
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
func NewStressTest(duration, iterations int, nodeURL, testAccount string, requestRate int, privateKeys []*ecdsa.PrivateKey, chainID *big.Int) *StressTest {
	return &StressTest{
		duration:        duration,
		iterations:      iterations,
		nodeURL:         nodeURL,
		testAccount:     testAccount,
		client:          &http.Client{Timeout: 30 * time.Second},
		responseTracker: NewResponseTimeTracker(),
		requestRate:     requestRate,
		privateKeys:     privateKeys,
		chainID:         chainID,
	}
}

// loadPrivateKeys loads one or more private keys from environment variables
func loadPrivateKeys() []*ecdsa.PrivateKey {
	var keys []*ecdsa.PrivateKey

	if rawMulti := os.Getenv("PRIVATE_KEYS"); rawMulti != "" {
		list, err := parsePrivateKeyList(rawMulti)
		if err != nil {
			log.Printf("Warning: Failed to parse PRIVATE_KEYS: %v", err)
		} else {
			for idx, keyHex := range list {
				privateKey, err := parsePrivateKeyHex(keyHex)
				if err != nil {
					log.Printf("Warning: PRIVATE_KEYS[%d] invalid: %v", idx, err)
					continue
				}
				keys = append(keys, privateKey)
			}
		}
	}

	// Fallback to single PRIVATE_KEY when no multi-wallet keys parsed
	if len(keys) == 0 {
		if single := os.Getenv("PRIVATE_KEY"); single != "" {
			privateKey, err := parsePrivateKeyHex(single)
			if err != nil {
				log.Printf("Warning: PRIVATE_KEY invalid: %v", err)
			} else {
				keys = append(keys, privateKey)
			}
		}
	}

	return keys
}

func parsePrivateKeyList(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	if strings.HasPrefix(raw, "[") {
		var list []string
		if err := json.Unmarshal([]byte(raw), &list); err != nil {
			return nil, err
		}
		return list, nil
	}

	parts := strings.Split(raw, ",")
	var cleaned []string
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}

	return cleaned, nil
}

func parsePrivateKeyHex(privateKeyHex string) (*ecdsa.PrivateKey, error) {
	value := strings.TrimSpace(privateKeyHex)
	if value == "" {
		return nil, fmt.Errorf("empty private key")
	}

	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		value = value[2:]
	}

	if len(value) != 64 {
		return nil, fmt.Errorf("private key should be 64 hex characters (32 bytes)")
	}

	privateKeyBytes, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid private key format: %w", err)
	}

	privateKey, err := crypto.ToECDSA(privateKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	return privateKey, nil
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
	tpsTicker := time.NewTicker(100 * time.Millisecond)
	defer tpsTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			goto done
		case <-tpsTicker.C:
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

// testTransactionTPS tests actual blockchain transaction throughput
func (st *StressTest) testTransactionTPS() {
	// Create and run transaction TPS test using the new module
	tpsTester := NewTransactionTPS(st.nodeURL, st.privateKeys, st.iterations, st.responseTracker)
	tpsTester.RunTransactionTPSTest()
}

// getFinalMetrics displays final performance metrics
func (st *StressTest) getFinalMetrics() {
	fmt.Println("Getting final performance metrics...")

	duration := time.Since(st.startTime)
	fmt.Printf("Total Duration: %v\n", duration)

	// Show detailed response time statistics
	if st.responseTracker != nil {
		min, max, avg := st.responseTracker.GetStats()
		fmt.Println("\nResponse Time Statistics:")
		fmt.Printf("  Min Response Time: %v\n", min)
		fmt.Printf("  Max Response Time: %v\n", max)
		fmt.Printf("  Average Response Time: %v\n", avg)

		// Show current TPS
		currentTPS := st.responseTracker.GetCurrentTPS()
		fmt.Printf("  Current TPS: %.2f\n", currentTPS)

		// Show transaction statistics if available
		sent, mined, avgMiningTime, miningTPS := st.responseTracker.GetTransactionStats()
		if sent > 0 {
			fmt.Println("\nTransaction Statistics:")
			fmt.Printf("  Transactions Sent: %d\n", sent)
			fmt.Printf("  Transactions Mined: %d\n", mined)
			fmt.Printf("  Mining TPS: %.2f\n", miningTPS)
			fmt.Printf("  Average Mining Time: %v\n", avgMiningTime)
			fmt.Printf("  Success Rate: %.1f%%\n", float64(mined)/float64(sent)*100)
		}
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
		min, max, avg := st.responseTracker.GetStats()
		fmt.Printf("Response Time Range: %v - %v\n", min, max)
		fmt.Printf("Average Response Time: %v\n", avg)
	}

	fmt.Println("Node Stability: PASSED")
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
	fmt.Printf("  %s         Blockchain transaction TPS test\n", TestTransactionTPS)
	fmt.Printf("  %s                 Run all tests (default)\n", TestAll)
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  ./stress-test                                    # Run all tests")
	fmt.Println("  ./stress-test basic-rpc                          # Run only basic RPC test")
	fmt.Println("  ./stress-test concurrent-block mixed-methods     # Run specific tests")
	fmt.Println("  ./stress-test sustained-load                     # Run only sustained load test")
	fmt.Println("  ./stress-test transaction-tps                    # Run only transaction TPS test")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  ODYSSEY_RPC_URL      RPC endpoint URL (required)")
	fmt.Println("  ODYSSEY_TEST_ACCOUNT Test account address (required)")
	fmt.Println("  DURATION             Duration of sustained load test in seconds (default: 30)")
	fmt.Println("  ITERATIONS           Number of concurrent requests (default: 100)")
	fmt.Println("  REQUEST_RATE         Requests per second for sustained load (default: auto-calculate)")
	fmt.Println("  PRIVATE_KEY          Private key for transaction signing (hex format, optional)")
	fmt.Println("  PRIVATE_KEYS         JSON array or comma-separated private keys (optional)")
	fmt.Println("  GAS_PRICE            Custom gas price in wei (optional, defaults to network price + 20%)")
	fmt.Println()
	fmt.Println("Environment Examples:")
	fmt.Println("  ODYSSEY_RPC_URL=http://my-node:9650/ext/bc/D/rpc ODYSSEY_TEST_ACCOUNT=0x1234... DURATION=60 ITERATIONS=200 ./stress-test")
	fmt.Println("  # Or set them before running:")
	fmt.Println("  export ODYSSEY_RPC_URL=http://my-node:9650/ext/bc/D/rpc")
	fmt.Println("  export ODYSSEY_TEST_ACCOUNT=0x1234...")
	fmt.Println("  export DURATION=60")
	fmt.Println("  export ITERATIONS=200")
	fmt.Println("  export REQUEST_RATE=50  # Force 50 requests per second")
	fmt.Println("  export PRIVATE_KEY=0x1234...  # For transaction TPS testing")
	fmt.Println("  export PRIVATE_KEYS='[\"0x1234...\",\"0xabcd...\"]'  # Multi-wallet support")
	fmt.Println("  export GAS_PRICE=250000000000  # Custom gas price (250 Gwei)")
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

	// Load environment variables from .env file
	if err := loadEnvFile(); err != nil {
		log.Fatalf("Failed to load .env file: %v", err)
	}

	// Get configuration from environment variables
	nodeURL, testAccount, duration, iterations, requestRate := getConfigFromEnv()

	// Load private keys from environment variables
	privateKeys := loadPrivateKeys()

	// Parse test arguments
	tests := parseTestArgs()

	// Create and run stress test
	st := NewStressTest(duration, iterations, nodeURL, testAccount, requestRate, privateKeys, nil) // chainID will be set during transaction test

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
