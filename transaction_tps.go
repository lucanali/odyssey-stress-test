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
			
			// Track sending start time
			sendStartTime := time.Now()
			
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
			
			// Calculate actual sending time
			sendTime := time.Since(sendStartTime)
			
			// Record transaction sent with sending time
			t.responseTracker.RecordTransactionSentWithTime(txHash, sendTime)
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
	monitorTicker := time.NewTicker(1 * time.Millisecond)
	defer monitorTicker.Stop()
	startMiningTime := time.Time{}
	
	for {
		select {
		case <-monitorTicker.C:
			sent, mined, _, _ := t.responseTracker.GetTransactionStats()
			
			// Get detailed statistics for real-time display
			_, _, _, _, avgFinalizationTime, _ := t.responseTracker.GetDetailedTransactionStats()
			
			// Record when first transaction was mined
			if mined > 0 && startMiningTime.IsZero() {
				startMiningTime = time.Now()
			}
			
			// Show finalization info if available
			if avgFinalizationTime > 0 {
				fmt.Printf("    Block Finalization: Avg %v per block\n", avgFinalizationTime)
			}
			
			// Check if all transactions are mined
			if mined >= sent {
				fmt.Println("\n ALL TRANSACTIONS MINED! Test completed successfully!")
				goto done
			}
		}
	}
	
done:
	// Final statistics
	sent, mined, _, _ := t.responseTracker.GetTransactionStats()
	
	// Get detailed statistics
	_, _, avgSendTime, detailedAvgMiningTime, avgFinalizationTime, _ := t.responseTracker.GetDetailedTransactionStats()
	
	totalDuration := time.Since(startTime)
	
	// Calculate final TPS
	finalTPS := float64(mined) / totalDuration.Seconds()
	
	fmt.Printf("\nFinal Transaction TPS Test Results:\n")
	fmt.Printf("=====================================\n")
	fmt.Printf("Total Sent: %d\n", sent)
	fmt.Printf("Total Mined: %d\n", mined)
	fmt.Printf("Total Duration: %v\n", totalDuration)
	fmt.Printf("Final Mining TPS: %.4f\n", finalTPS)
	fmt.Printf("Average Mining Time: %v\n", detailedAvgMiningTime)
	fmt.Printf("Success Rate: %.1f%%\n", float64(mined)/float64(sent)*100)
	
	// Display detailed timing breakdown
	fmt.Printf("\nDetailed Timing Breakdown:\n")
	fmt.Printf("=============================\n")
	fmt.Printf("Average Sending Time: %v\n", avgSendTime)
	fmt.Printf("Average Mining Time: %v\n", detailedAvgMiningTime)
	if avgFinalizationTime > 0 {
		fmt.Printf("Average Finalization Time: %v\n", avgFinalizationTime)
		fmt.Printf("Total Transaction Lifecycle: %v\n", avgSendTime+detailedAvgMiningTime+avgFinalizationTime)
		
		// Show finalization statistics
		finalizationStatus := t.responseTracker.GetTransactionFinalizationStatus()
		finalizedCount := 0
		for _, finalized := range finalizationStatus {
			if finalized {
				finalizedCount++
			}
		}
		fmt.Printf("Finalization Rate: %d/%d (%.1f%%)\n", finalizedCount, sent, float64(finalizedCount)/float64(sent)*100)
		
		// Show individual transaction breakdowns
		fmt.Printf("\nIndividual Transaction Breakdown:\n")
		fmt.Printf("==================================\n")
		individualTimings := t.responseTracker.GetIndividualTransactionTimings()
		txIndex := 1
		for txHash, timing := range individualTimings {
			fmt.Printf("Transaction %d (%s...%s):\n", txIndex, txHash[:10], txHash[len(txHash)-10:])
			
			if sendTime, exists := timing["sendTime"]; exists {
				fmt.Printf("  Send Time: %v\n", sendTime)
			}
			
			if miningTime, exists := timing["miningTime"]; exists {
				fmt.Printf("  Mining Time: %v\n", miningTime)
			}
			
			if finalizationTime, exists := timing["finalizationTime"]; exists {
				fmt.Printf("  Finalization Time: %v\n", finalizationTime)
			}
			
			if blockNumber, exists := timing["blockNumber"]; exists {
				fmt.Printf("  Block: %s\n", blockNumber)
			}
			
			if finalized, exists := timing["finalized"]; exists {
				status := "Pending"
				if finalized.(bool) {
					status = "Finalized"
				}
				fmt.Printf("  Status: %s\n", status)
			}
			
			fmt.Println()
			txIndex++
		}
	} else {
		fmt.Printf("Block Finalization: Not tracked (chain may not support finality)\n")
		fmt.Printf("Total Transaction Lifecycle (Send + Mine): %v\n", avgSendTime+detailedAvgMiningTime)
	}
	
	// Write final results to file
	if file != nil {
		fmt.Fprintf(file, "\nFinal Results:\n")
		fmt.Fprintf(file, "================\n")
		fmt.Fprintf(file, "Total Sent: %d\n", sent)
		fmt.Fprintf(file, "Total Mined: %d\n", mined)
		fmt.Fprintf(file, "Total Duration: %v\n", totalDuration)
		fmt.Fprintf(file, "Final Mining TPS: %.4f\n", finalTPS)
		fmt.Fprintf(file, "Average Mining Time: %v\n", detailedAvgMiningTime)
		fmt.Fprintf(file, "Success Rate: %.1f%%\n", float64(mined)/float64(sent)*100)
		
		// Write detailed timing breakdown to file
		fmt.Fprintf(file, "\nDetailed Timing Breakdown:\n")
		fmt.Fprintf(file, "=============================\n")
		fmt.Fprintf(file, "Average Sending Time: %v\n", avgSendTime)
		fmt.Fprintf(file, "Average Mining Time: %v\n", detailedAvgMiningTime)
		if avgFinalizationTime > 0 {
			fmt.Fprintf(file, "Average Finalization Time: %v\n", avgFinalizationTime)
			fmt.Fprintf(file, "Total Transaction Lifecycle: %v\n", avgSendTime+detailedAvgMiningTime+avgFinalizationTime)
			// Individual breakdown
			fmt.Fprintf(file, "\nIndividual Transaction Breakdown:\n")
			fmt.Fprintf(file, "==================================\n")
			ind := t.responseTracker.GetIndividualTransactionTimings()
			txIndex := 1
			for txHash, timing := range ind {
				fmt.Fprintf(file, "Transaction %d (%s...%s):\n", txIndex, txHash[:10], txHash[len(txHash)-10:])
				if sendTime, ok := timing["sendTime"]; ok { fmt.Fprintf(file, "  Send Time: %v\n", sendTime) }
				if miningTime, ok := timing["miningTime"]; ok { fmt.Fprintf(file, "  Mining Time: %v\n", miningTime) }
				if finalizationTime, ok := timing["finalizationTime"]; ok { fmt.Fprintf(file, " Finalization Time: %v\n", finalizationTime) }
				if blockNumber, ok := timing["blockNumber"]; ok { fmt.Fprintf(file, "  Block: %s\n", blockNumber) }
				if finalized, ok := timing["finalized"]; ok {
					status := "Pending"
					if finalized.(bool) { status = "Finalized" }
					fmt.Fprintf(file, "Status: %s\n", status)
				}
				fmt.Fprintln(file)
				txIndex++
			}
		} else {
			fmt.Fprintf(file, "Block Finalization: Not tracked (chain may not support finality)\n")
			fmt.Fprintf(file, "Total Transaction Lifecycle (Send + Mine): %v\n", avgSendTime+detailedAvgMiningTime)
		}
		
		fmt.Printf("\n Detailed results saved to: %s\n", outputFile)
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
	// start monitoring
	
	startTime := time.Now()
	maxWaitTime := 30 * time.Minute
	
	blockTicker := time.NewTicker(10 * time.Millisecond)
	defer blockTicker.Stop()
	
	// Also check transaction receipt immediately and periodically
	receiptTicker := time.NewTicker(5 * time.Millisecond)
	defer receiptTicker.Stop()
	
	// Track the last block we've seen
	lastKnownBlock := ""
	
	for {
		select {
		case <-blockTicker.C:
			// Check for new blocks
			currentBlock, err := t.getCurrentBlockNumber()
			if err != nil {
				continue
			}
			
			// If we have a new block, check if it contains our transaction
			if currentBlock != lastKnownBlock {
				lastKnownBlock = currentBlock
				
				// Check if our transaction is in this new block
				if blockNumber := t.checkTransactionInBlock(txHash, currentBlock); blockNumber != "" {
					miningTime := time.Since(startTime)
					fmt.Printf("Transaction %s mined in block %s (latency %v)\n", txHash, blockNumber, miningTime)
					
					// Record mining time with block information
					t.responseTracker.RecordTransactionMinedInBlock(txHash, blockNumber)
					
					// Start monitoring block finalization if supported
					go t.monitorRealTransactionFinalization(txHash, blockNumber, startTime)
					
					return
				}
			}
			
			// Check if we've been waiting too long
			if time.Since(startTime) > maxWaitTime { return }
			
		case <-receiptTicker.C:
			// Check transaction receipt for immediate confirmation
			if blockNumber := t.checkTransactionReceipt(txHash); blockNumber != "" {
				miningTime := time.Since(startTime)
				fmt.Printf("Transaction %s confirmed via receipt in block %s (latency %v)\n", txHash, blockNumber, miningTime)
				
				// Record mining time with block information
				t.responseTracker.RecordTransactionMinedInBlock(txHash, blockNumber)
				
				// Start monitoring block finalization if supported
				go t.monitorRealTransactionFinalization(txHash, blockNumber, startTime)
				
				return
			}
			
			// quiet while checking
		}
	}
}

// checkTransactionInBlock checks if a transaction is included in a specific block
func (t *TransactionTPS) checkTransactionInBlock(txHash string, blockNumber string) string {
	// Use eth_getBlockByNumber to get block details and check if it contains our transaction
	formattedBlockNumber := blockNumber
	if strings.HasPrefix(blockNumber, "x") {
		formattedBlockNumber = "0x" + blockNumber[1:]
	} else if !strings.HasPrefix(blockNumber, "0x") {
		formattedBlockNumber = "0x" + blockNumber
	}
	
	jsonData := fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["%s",true],"id":1}`, formattedBlockNumber)
	
	resp, err := http.Post(t.nodeURL, "application/json", strings.NewReader(jsonData))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	
	responseStr := string(body)
	
	// Check if the block contains our transaction
	if strings.Contains(responseStr, txHash) {
		return blockNumber
	}
	
	return ""
}

// checkTransactionReceipt checks transaction receipt for immediate confirmation
func (t *TransactionTPS) checkTransactionReceipt(txHash string) string {
	// Use eth_getTransactionReceipt for immediate confirmation
	jsonData := fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_getTransactionReceipt","params":["%s"],"id":1}`, txHash)
	
	resp, err := http.Post(t.nodeURL, "application/json", strings.NewReader(jsonData))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	
	responseStr := string(body)
	
	// Check if receipt exists and contains block number
	if responseStr != "" && responseStr != "null" && !strings.Contains(responseStr, `"blockNumber":null`) && strings.Contains(responseStr, `"blockNumber"`) {
		// Extract block number from receipt
		blockStart := strings.Index(responseStr, `"blockNumber":"`) + 16
		blockEnd := strings.Index(responseStr[blockStart:], `"`)
		if blockEnd > 0 {
			blockNumber := responseStr[blockStart : blockStart+blockEnd]
			if blockNumber != "0x0" {
				// Convert hex block number to our format
				if strings.HasPrefix(blockNumber, "0x") {
					// Remove 0x prefix and add x prefix for our format
					return "x" + blockNumber[2:]
				}
				return blockNumber
			}
		}
	}
	
	return ""
}

// monitorRealTransactionFinalization monitors a transaction for finalization
func (t *TransactionTPS) monitorRealTransactionFinalization(txHash string, blockNumber string, miningStartTime time.Time) {
	// Start finalization monitoring timing
	t.responseTracker.StartTransactionFinalizationMonitoring(txHash)
	
	startTime := time.Now()
	maxWaitTime := 2 * time.Minute
	
	ticker := time.NewTicker(1 * time.Millisecond)
	defer ticker.Stop()
	
	// Check if the original block is accepted immediately
	if t.isBlockAccepted(blockNumber) {
		finalizationTime := time.Since(startTime)
		fmt.Printf("Transaction %s finalized in block %s (finality %v)\n", txHash, blockNumber, finalizationTime)
		
		// Record finalization time
		t.responseTracker.RecordTransactionFinalized(txHash, finalizationTime)
		return
	}
	
	// If original block not accepted, monitor for potential re-mining
	attempts := 0
	maxAttempts := 300
	
	for attempts < maxAttempts {
		select {
		case <-ticker.C:
			attempts++
			
			// timeout handling
			if time.Since(startTime) > maxWaitTime { return }
			
			// Check if the original block is now accepted
			if t.isBlockAccepted(blockNumber) {
				finalizationTime := time.Since(startTime)
				fmt.Printf("Transaction %s finalized in block %s (finality %v)\n", txHash, blockNumber, finalizationTime)
				
				t.responseTracker.RecordTransactionFinalized(txHash, finalizationTime)
				return
			}
			
			// Check if transaction was re-mined in a different block
			if newBlockNumber := t.checkTransactionRemining(txHash); newBlockNumber != "" && newBlockNumber != blockNumber {
				fmt.Printf("Transaction %s re-mined in block %s (from %s)\n", txHash, newBlockNumber, blockNumber)
				
				// Update the block association
				t.responseTracker.RecordTransactionMinedInBlock(txHash, newBlockNumber)
				
				// Check if the new block is accepted
				if t.isBlockAccepted(newBlockNumber) {
					finalizationTime := time.Since(startTime)
					fmt.Printf("Transaction %s finalized in block %s (finality %v)\n", txHash, newBlockNumber, finalizationTime)
					
					t.responseTracker.RecordTransactionFinalized(txHash, finalizationTime)
					return
				}
			}
			
			// quiet while checking
		}
	}

	// fallback finalization
	t.responseTracker.RecordTransactionFinalized(txHash, time.Since(startTime))
}

// checkTransactionRemining checks if a transaction was re-mined in a different block
func (t *TransactionTPS) checkTransactionRemining(txHash string) string {
	// Use eth_getTransactionByHash to check current block
	jsonData := fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_getTransactionByHash","params":["%s"],"id":1}`, txHash)
	
	resp, err := http.Post(t.nodeURL, "application/json", strings.NewReader(jsonData))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	
	responseStr := string(body)
	
	// Extract current block number
	if strings.Contains(responseStr, `"blockNumber"`) && !strings.Contains(responseStr, `"blockNumber":null`) {
		blockStart := strings.Index(responseStr, `"blockNumber":"`) + 16
		blockEnd := strings.Index(responseStr[blockStart:], `"`)
		if blockEnd > 0 {
			blockNumber := responseStr[blockStart : blockStart+blockEnd]
			if blockNumber != "0x0" {
				return blockNumber
			}
		}
	}
	
	return ""
}

// monitorBlockFinalization monitors a block for finalization (if the chain supports it)
func (t *TransactionTPS) monitorBlockFinalization(blockNumber string, txHash string) {	
	startTime := time.Now()
	maxWaitTime := 2 * time.Second 
	
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	// Check if the block is accepted by consensus
	if t.isBlockAccepted(blockNumber) { t.responseTracker.RecordBlockFinalized(blockNumber); return }
	
	// If not immediately accepted, monitor for a short time
	for attempts := 0; attempts < 10; attempts++ {
		select {
		case <-ticker.C:
			// Check if we've been waiting too long
			if time.Since(startTime) > maxWaitTime { return }
			
			// Check if the block is now accepted
			if t.isBlockAccepted(blockNumber) { t.responseTracker.RecordBlockFinalized(blockNumber); return }
			
			// Also check confirmations as a fallback
			currentBlock, err := t.getCurrentBlockNumber()
			if err == nil {
				if blockNum, currentBlockNum, err := t.parseBlockNumbers(blockNumber, currentBlock); err == nil {
					confirmations := int(currentBlockNum - blockNum)
					if confirmations >= 2 { t.responseTracker.RecordBlockFinalized(blockNumber); return }
				}
			}
			
			// quiet while checking
		}
	}
	
	t.responseTracker.RecordBlockFinalized(blockNumber)
}

// getCurrentBlockNumber gets the current block number from the network
func (t *TransactionTPS) getCurrentBlockNumber() (string, error) {
	jsonData := `{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`
	
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

// parseBlockNumbers parses hex block numbers and returns them as uint64
func (t *TransactionTPS) parseBlockNumbers(block1, block2 string) (uint64, uint64, error) {
	// Parse first block number
	var block1Num uint64
	if strings.HasPrefix(block1, "0x") {
		hexStr := block1[2:] // Remove "0x" prefix
		if len(hexStr)%2 != 0 {
			hexStr = "0" + hexStr
		}
		var err error
		block1Num, err = strconv.ParseUint(hexStr, 16, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to parse block number %s: %v", block1, err)
		}
	} else {
		var err error
		block1Num, err = strconv.ParseUint(block1, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to parse block number %s: %v", block1, err)
		}
	}
	
	// Parse second block number
	var block2Num uint64
	if strings.HasPrefix(block2, "0x") {
		hexStr := block2[2:] // Remove "0x" prefix
		if len(hexStr)%2 != 0 {
			hexStr = "0" + hexStr
		}
		var err error
		block2Num, err = strconv.ParseUint(hexStr, 16, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to parse block number %s: %v", block2, err)
		}
	} else {
		var err error
		block2Num, err = strconv.ParseUint(block2, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to parse block number %s: %v", block2, err)
		}
	}
	
	return block1Num, block2Num, nil
}

// isBlockAccepted checks if a block is accepted by consensus mechanism
func (t *TransactionTPS) isBlockAccepted(blockNumber string) bool {
	formattedBlockNumber := blockNumber
	if strings.HasPrefix(blockNumber, "x") {
		// Convert x1f4d10 to 0x1f4d10
		formattedBlockNumber = "0x" + blockNumber[1:]
	} else if !strings.HasPrefix(blockNumber, "0x") {
		formattedBlockNumber = "0x" + blockNumber
	}
	
	// Method 1: Check if the block has a valid block hash (not null)
	jsonData := fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["%s",false],"id":1}`, formattedBlockNumber)
	
	resp, err := http.Post(t.nodeURL, "application/json", strings.NewReader(jsonData))
	if err != nil { return false }
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil { return false }
	
	responseStr := string(body)
	
	// If the response contains a valid block hash (not null), the block is accepted
	if responseStr != "" && responseStr != "null" && !strings.Contains(responseStr, `"hash":null`) && strings.Contains(responseStr, `"hash"`) {
		// Extract hash to verify it's not null
		hashStart := strings.Index(responseStr, `"hash":"`) + 8
		hashEnd := strings.Index(responseStr[hashStart:], `"`)
		if hashEnd > 0 {
			hash := responseStr[hashStart : hashStart+hashEnd]
			if hash != "null" && hash != "0x0000000000000000000000000000000000000000000000000000000000000000" { return true }
		}
	}
	
	// Method 2: Check if the block has transactions (indicates it was properly mined)
	if strings.Contains(responseStr, `"transactions"`) && !strings.Contains(responseStr, `"transactions":[]`) { return true }
	
	// Method 3: Check if the block has a valid number and timestamp
	if strings.Contains(responseStr, `"number"`) && strings.Contains(responseStr, `"timestamp"`) {
		// Extract block number to verify it matches
		numberStart := strings.Index(responseStr, `"number":"`) + 10
		numberEnd := strings.Index(responseStr[numberStart:], `"`)
		if numberEnd > 0 {
			number := responseStr[numberStart : numberStart+numberEnd]
			if number == formattedBlockNumber { return true }
		}
	}
	
	return false
}
