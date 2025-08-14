package main

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// TransactionTPS handles transaction throughput testing
type TransactionTPS struct {
	nodeURL        string
	privateKey     *ecdsa.PrivateKey
	iterations     int
	responseTracker *ResponseTimeTracker
}

// NewTransactionTPS creates a new TransactionTPS instance
func NewTransactionTPS(nodeURL string, privateKey *ecdsa.PrivateKey, iterations int, responseTracker *ResponseTimeTracker) *TransactionTPS {
	return &TransactionTPS{
		nodeURL:        nodeURL,
		privateKey:     privateKey,
		iterations:     iterations,
		responseTracker: responseTracker,
	}
}

// RunTransactionTPSTest runs the transaction TPS test
func (t *TransactionTPS) RunTransactionTPSTest() {
	fmt.Println("Testing blockchain transaction TPS...")
	
	if t.privateKey == nil {
		fmt.Println("Warning: No private key configured, skipping transaction TPS test")
		fmt.Println("Set PRIVATE_KEY environment variable to enable transaction testing")
		return
	}
	
	// Check network status first
	fmt.Println("Checking network status...")
	fmt.Println("Note: Network status check simplified - proceeding with transaction test")
	
	fmt.Printf("Sending %d transactions to measure throughput...\n", t.iterations)
	
	// Get current nonce from network
	nonce, err := t.getCurrentNonce()
	if err != nil {
		fmt.Printf("Failed to get current nonce: %v\n", err)
		return
	}
	fmt.Printf("Starting with nonce: %d\n", nonce)
	
	startTime := time.Now()
	
	// Send transactions concurrently
	var wg sync.WaitGroup
	results := make(chan string, t.iterations)
	successCount := 0
	errorCount := 0
	
	for i := 0; i < t.iterations; i++ {
		wg.Add(1)
		go func(txNonce uint64) {
			defer wg.Done()
			
			// Create and send transaction
			txHash, err := t.sendRealTransaction(txNonce)
			if err != nil {
				// Check if it's a duplicate nonce error
				if strings.Contains(err.Error(), "already known") {
					fmt.Printf("Transaction with nonce %d already exists, skipping\n", txNonce)
				} else {
					fmt.Printf("Failed to send transaction with nonce %d: %v\n", txNonce, err)
				}
				errorCount++
				return
			}
			
			// Record transaction sent
			t.responseTracker.RecordTransactionSent(txHash)
			results <- txHash
			successCount++
			
			// Monitor transaction mining
			go t.monitorRealTransaction(txHash)
			
		}(nonce + uint64(i))
	}
	
	// Wait for all transactions to be sent
	wg.Wait()
	close(results)
	
	// Count sent transactions
	sentCount := 0
	for range results {
		sentCount++
	}
	
	fmt.Printf("Sent %d transactions successfully, %d errors in %.2fs\n", successCount, errorCount, time.Since(startTime).Seconds())
	
	// If no transactions were sent successfully, exit early
	if successCount == 0 {
		fmt.Println("No transactions were sent successfully. Exiting test.")
		return
	}
	
	// Monitor mining progress until ALL transactions are mined
	fmt.Println("Monitoring transaction mining until ALL transactions are mined...")
	fmt.Println("This may take several minutes depending on network conditions...")
	
	// Create output file for detailed results
	outputFile := fmt.Sprintf("transaction_tps_results_%s.txt", time.Now().Format("20060102_150405"))
	file, err := os.Create(outputFile)
	if err != nil {
		fmt.Printf("Warning: Could not create output file: %v\n", err)
	} else {
		defer file.Close()
		fmt.Fprintf(file, "Odyssey Chain Transaction TPS Test Results\n")
		fmt.Fprintf(file, "==========================================\n")
		fmt.Fprintf(file, "Test Date: %s\n", time.Now().Format("2006-01-02 15:04:05"))
		fmt.Fprintf(file, "Node URL: %s\n", t.nodeURL)
		fmt.Fprintf(file, "Chain ID: 131313\n")
		fmt.Fprintf(file, "Total Transactions: %d\n", sentCount)
		fmt.Fprintf(file, "Start Time: %s\n", startTime.Format("15:04:05"))
		fmt.Fprintf(file, "\nMining Progress:\n")
		fmt.Fprintf(file, "================\n")
	}
	
	// Monitor until all transactions are mined
	monitorTicker := time.NewTicker(10 * time.Second)
	defer monitorTicker.Stop()
	
	lastMinedCount := 0
	lastUpdateTime := time.Now()
	startMiningTime := time.Time{}
	
	for {
		select {
		case <-monitorTicker.C:
			sent, mined, avgMiningTime, _ := t.responseTracker.GetTransactionStats()
			
			// Record when first transaction was mined
			if mined > 0 && startMiningTime.IsZero() {
				startMiningTime = time.Now()
			}
			
			// Calculate current mining rate
			timeSinceLastUpdate := time.Since(lastUpdateTime).Seconds()
			recentMined := mined - lastMinedCount
			currentMiningRate := float64(recentMined) / timeSinceLastUpdate
			
			// Calculate overall progress
			progress := float64(mined) / float64(sent) * 100
			elapsed := time.Since(startTime)
			
			// Calculate estimated time remaining
			var eta time.Duration
			if currentMiningRate > 0 && mined > 0 {
				remaining := sent - mined
				etaSeconds := float64(remaining) / currentMiningRate
				eta = time.Duration(etaSeconds) * time.Second
			}
			
			// Display progress with ETA
			if eta > 0 {
				fmt.Printf("[%s] Progress: %d/%d (%.1f%%) | Recent Mining Rate: %.2f TPS | Avg Mining Time: %v | Elapsed: %v | ETA: %v\n",
					time.Now().Format("15:04:05"), mined, sent, progress, currentMiningRate, avgMiningTime, elapsed, eta)
			} else {
				fmt.Printf("[%s] Progress: %d/%d (%.1f%%) | Recent Mining Rate: %.2f TPS | Avg Mining Time: %v | Elapsed: %v | ETA: Calculating...\n",
					time.Now().Format("15:04:05"), mined, sent, progress, currentMiningRate, avgMiningTime, elapsed)
			}
			
			// Write to file
			if file != nil {
				if eta > 0 {
					fmt.Fprintf(file, "[%s] Progress: %d/%d (%.1f%%) | Recent Mining Rate: %.2f TPS | Avg Mining Time: %v | Elapsed: %v | ETA: %v\n",
						time.Now().Format("15:04:05"), mined, sent, progress, currentMiningRate, avgMiningTime, elapsed, eta)
				} else {
					fmt.Fprintf(file, "[%s] Progress: %d/%d (%.1f%%) | Recent Mining Rate: %.2f TPS | Avg Mining Time: %v | Elapsed: %v | ETA: Calculating...\n",
						time.Now().Format("15:04:05"), mined, sent, progress, currentMiningRate, avgMiningTime, elapsed)
				}
			}
			
			// Check if all transactions are mined
			if mined >= sent {
				fmt.Println("\n🎉 ALL TRANSACTIONS MINED! Test completed successfully!")
				goto done
			}
			
			// Update counters
			lastMinedCount = mined
			lastUpdateTime = time.Now()
		}
	}
	
done:
	// Final statistics
	sent, mined, avgMiningTime, _ := t.responseTracker.GetTransactionStats()
	totalDuration := time.Since(startTime)
	
	// Calculate final TPS
	finalTPS := float64(mined) / totalDuration.Seconds()
	
	fmt.Printf("\n🎯 Final Transaction TPS Test Results:\n")
	fmt.Printf("=====================================\n")
	fmt.Printf("Total Sent: %d\n", sent)
	fmt.Printf("Total Mined: %d\n", mined)
	fmt.Printf("Total Duration: %v\n", totalDuration)
	fmt.Printf("Final Mining TPS: %.4f\n", finalTPS)
	fmt.Printf("Average Mining Time: %v\n", avgMiningTime)
	fmt.Printf("Success Rate: %.1f%%\n", float64(mined)/float64(sent)*100)
	
	// Write final results to file
	if file != nil {
		fmt.Fprintf(file, "\n🎯 Final Results:\n")
		fmt.Fprintf(file, "================\n")
		fmt.Fprintf(file, "Total Sent: %d\n", sent)
		fmt.Fprintf(file, "Total Mined: %d\n", mined)
		fmt.Fprintf(file, "Total Duration: %v\n", totalDuration)
		fmt.Fprintf(file, "Final Mining TPS: %.4f\n", finalTPS)
		fmt.Fprintf(file, "Average Mining Time: %v\n", avgMiningTime)
		fmt.Fprintf(file, "Success Rate: %.1f%%\n", float64(mined)/float64(sent)*100)
		fmt.Fprintf(file, "\nOutput file: %s\n", outputFile)
		
		fmt.Printf("\n📁 Detailed results saved to: %s\n", outputFile)
	}
	
	fmt.Println()
}

// getCurrentNonce gets the current nonce for the test account
func (t *TransactionTPS) getCurrentNonce() (uint64, error) {
	// Get the public key from private key
	publicKey := t.privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return 0, fmt.Errorf("failed to get public key")
	}
	
	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)
	
	// Get current nonce from network
	jsonData := fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_getTransactionCount","params":["%s","latest"],"id":1}`, fromAddress.Hex())
	
	resp, err := http.Post(t.nodeURL, "application/json", strings.NewReader(jsonData))
	if err != nil {
		return 0, fmt.Errorf("HTTP error: %v", err)
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %v", err)
	}
	
	// Parse the response manually
	var result struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  string `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("failed to parse JSON: %v", err)
	}
	
	if result.Error != nil {
		return 0, fmt.Errorf("RPC error: %s", result.Error.Message)
	}
	
	// Parse hex nonce
	if strings.HasPrefix(result.Result, "0x") {
		hexStr := result.Result[2:] // Remove "0x" prefix
		// Pad with leading zero if odd length
		if len(hexStr)%2 != 0 {
			hexStr = "0" + hexStr
		}
		nonce, err := strconv.ParseUint(hexStr, 16, 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse hex nonce: %v", err)
		}
		return nonce, nil
	}
	
	return 0, fmt.Errorf("invalid hex string: %s", result.Result)
}

// sendRealTransaction sends a real transaction to the blockchain
func (t *TransactionTPS) sendRealTransaction(nonce uint64) (string, error) {
	// Get the public key from private key
	publicKey := t.privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("failed to get public key")
	}
	
	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)
	
	// Get gas price
	gasPrice, err := t.getGasPrice()
	if err != nil {
		return "", fmt.Errorf("failed to get gas price: %v", err)
	}
	
	// Create transaction (0 ETH to self)
	tx := types.NewTransaction(
		nonce,                    // nonce
		fromAddress,              // to (self)
		big.NewInt(0),           // value (0 ETH)
		21000,                   // gas limit
		gasPrice,                 // gas price
		nil,                      // data
	)
	
	// Sign transaction with hardcoded chain ID (131313 for local testnet)
	chainID := big.NewInt(131313)
	signer := types.NewEIP155Signer(chainID)
	signedTx, err := types.SignTx(tx, signer, t.privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %v", err)
	}
	
	// Encode to hex
	encodedTx, err := signedTx.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("failed to encode transaction: %v", err)
	}
	
	txHex := "0x" + hex.EncodeToString(encodedTx)
	
	// Send transaction
	jsonData := fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_sendRawTransaction","params":["%s"],"id":1}`, txHex)
	
	resp, err := http.Post(t.nodeURL, "application/json", strings.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("HTTP error: %v", err)
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %v", err)
	}
	
	// Parse the response manually
	var result struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  string `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse JSON: %v", err)
	}
	
	if result.Error != nil {
		return "", fmt.Errorf("RPC error: %s", result.Error.Message)
	}
	
	return result.Result, nil
}

// getGasPrice gets the current gas price from the network
func (t *TransactionTPS) getGasPrice() (*big.Int, error) {
	// Check if GAS_PRICE environment variable is set
	if gasPriceStr := os.Getenv("GAS_PRICE"); gasPriceStr != "" {
		gasPrice, ok := new(big.Int).SetString(gasPriceStr, 10)
		if ok {
			fmt.Printf("Using GAS_PRICE from environment: %s wei\n", gasPrice.String())
			return gasPrice, nil
		}
	}
	
	// Get gas price from network
	jsonData := `{"jsonrpc":"2.0","method":"eth_gasPrice","params":[],"id":1}`
	
	resp, err := http.Post(t.nodeURL, "application/json", strings.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("HTTP error: %v", err)
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}
	
	// Parse the response manually
	var result struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  string `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %v", err)
	}
	
	if result.Error != nil {
		return nil, fmt.Errorf("RPC error: %s", result.Error.Message)
	}
	
	// Parse hex gas price
	if strings.HasPrefix(result.Result, "0x") {
		hexStr := result.Result[2:] // Remove "0x" prefix
		// Pad with leading zero if odd length
		if len(hexStr)%2 != 0 {
			hexStr = "0" + hexStr
		}
		gasPrice, err := strconv.ParseUint(hexStr, 16, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse hex gas price: %v", err)
		}
		
		// Add 20% buffer to ensure transactions go through
		bufferedGasPrice := new(big.Int).Mul(big.NewInt(int64(gasPrice)), big.NewInt(120))
		bufferedGasPrice.Div(bufferedGasPrice, big.NewInt(100))
		
		fmt.Printf("Using gas price: %s wei (%.2f Gwei)\n", bufferedGasPrice.String(), new(big.Float).Quo(new(big.Float).SetInt(bufferedGasPrice), big.NewFloat(1000000000)))
		return bufferedGasPrice, nil
	}
	
	return nil, fmt.Errorf("invalid hex string: %s", result.Result)
}

// monitorRealTransaction monitors a real transaction until it's mined
func (t *TransactionTPS) monitorRealTransaction(txHash string) {
	fmt.Printf("Debug: Starting to monitor transaction: %s\n", txHash)
	
	// Poll for transaction mining
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	startTime := time.Now()
	maxWaitTime := 30 * time.Minute // Wait up to 30 minutes
	
	for {
		select {
		case <-ticker.C:
			// Check if we've been waiting too long
			if time.Since(startTime) > maxWaitTime {
				fmt.Printf("Debug: Transaction %s monitoring timed out after %v\n", txHash, maxWaitTime)
				return
			}
			
			// Use eth_getTransactionByHash to check if mined
			jsonData := fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_getTransactionByHash","params":["%s"],"id":1}`, txHash)
			
			resp, err := http.Post(t.nodeURL, "application/json", strings.NewReader(jsonData))
			if err != nil {
				fmt.Printf("Debug: HTTP error for transaction %s: %v\n", txHash, err)
				continue
			}
			
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				fmt.Printf("Debug: Failed to read response for transaction %s: %v\n", txHash, err)
				continue
			}
			
			responseStr := string(body)
			
			// Check if response contains block number (indicating it's mined)
			if responseStr != "" && responseStr != "null" && !strings.Contains(responseStr, `"blockNumber":null`) && strings.Contains(responseStr, `"blockNumber"`) {
				// Extract block number from response string
				if strings.Contains(responseStr, `"blockNumber":"0x0"`) {
					// Still in mempool (block 0x0)
					continue
				}
				
				// Transaction is mined! Extract block number
				blockStart := strings.Index(responseStr, `"blockNumber":"`) + 16
				blockEnd := strings.Index(responseStr[blockStart:], `"`)
				if blockEnd > 0 {
					blockNumber := responseStr[blockStart : blockStart+blockEnd]
					fmt.Printf("Debug: Transaction %s mined in block %s\n", txHash, blockNumber)
					
					// Record mining time
					miningTime := time.Since(startTime)
					t.responseTracker.RecordTransactionMined(txHash)
					
					fmt.Printf("Debug: Transaction %s took %v to mine\n", txHash, miningTime)
					return
				}
			}
			
			// Check network status every few attempts
			if time.Since(startTime).Seconds() > 30 {
				fmt.Printf("Debug: Still waiting for transaction %s to be mined... (elapsed: %v)\n", txHash, time.Since(startTime))
			}
		}
	}
}
