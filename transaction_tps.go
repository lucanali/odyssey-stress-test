package main

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
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
	
	fmt.Printf("Sending %d transactions concurrently to measure throughput...\n", t.iterations)
	
	// Get current nonce from network
	nonce, err := t.getCurrentNonce()
	if err != nil {
		fmt.Printf("Failed to get current nonce: %v\n", err)
		return
	}
	fmt.Printf("Starting with nonce: %d\n", nonce)
	
	// Record start time
	startTime := time.Now()
	
	// Send ALL transactions immediately and concurrently
	fmt.Printf("Sending %d transactions concurrently...\n", t.iterations)
	
	// Use a channel to coordinate goroutines
	txChan := make(chan string, t.iterations)
	var wg sync.WaitGroup
	
	// Send all transactions at once
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
				return
			}
			
			// Calculate actual sending time
			sendTime := time.Since(sendStartTime)
			
			// Record transaction sent with sending time
			t.responseTracker.RecordTransactionSentWithTime(txHash, sendTime)
			
			fmt.Printf("Transaction %d sent: %s (send time: %v)\n", txNonce, txHash, sendTime)
			
			// Send transaction hash to channel
			txChan <- txHash
			
			// Start monitoring immediately after sending
			go t.monitorRealTransaction(txHash)
			
		}(nonce + uint64(i))
	}
	
	// Wait for all transactions to be sent
	wg.Wait()
	close(txChan)
	
	// Count sent transactions
	sentCount := 0
	for range txChan {
		sentCount++
	}
	
	sendDuration := time.Since(startTime)
	fmt.Printf("All %d transactions sent in %v (%.2f TPS)\n", sentCount, sendDuration, float64(sentCount)/sendDuration.Seconds())
	
	// If no transactions were sent successfully, exit early
	if sentCount == 0 {
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
		fmt.Fprintf(file, "Send Duration: %v\n", sendDuration)
		fmt.Fprintf(file, "Send TPS: %.2f\n", float64(sentCount)/sendDuration.Seconds())
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
			
			// Record when first transaction was mined
			if mined > 0 && startMiningTime.IsZero() {
				startMiningTime = time.Now()
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
	_, _, avgSendTime, detailedAvgMiningTime, _, _ := t.responseTracker.GetDetailedTransactionStats()
	
	// Wait for any pending transactions to finalize (with timeout)
	fmt.Println("Waiting for pending transactions to finalize...")
	timeout := 30 * time.Second
	startWait := time.Now()
	
	for time.Since(startWait) < timeout {
		finalizationStatus := t.responseTracker.GetTransactionFinalizationStatus()
		pendingCount := 0
		for _, finalized := range finalizationStatus {
			if !finalized {
				pendingCount++
			}
		}
		
		if pendingCount == 0 {
			fmt.Println("All transactions finalized!")
			break
		}
		
		fmt.Printf("Waiting... %d transactions still pending\n", pendingCount)
		time.Sleep(100 * time.Millisecond)
	}
	
	if time.Since(startWait) >= timeout {
		fmt.Printf("Timeout reached after %v - some transactions may still be pending\n", timeout)
	}
	
	// Calculate final TPS
	totalDuration := time.Since(startTime)
	finalTPS := float64(mined) / totalDuration.Seconds()
	
	fmt.Printf("\nFinal Transaction TPS Test Results:\n")
	fmt.Printf("=====================================\n")
	fmt.Printf("Total Sent: %d\n", sent)
	fmt.Printf("Total Mined: %d\n", mined)
	fmt.Printf("Send Duration: %v\n", sendDuration)
	fmt.Printf("Total Duration: %v\n", totalDuration)
	fmt.Printf("Send TPS: %.2f\n", float64(sent)/sendDuration.Seconds())
	fmt.Printf("Final Mining TPS: %.4f\n", finalTPS)
	fmt.Printf("Average Mining Time: %v\n", detailedAvgMiningTime)
	fmt.Printf("Success Rate: %.1f%%\n", float64(mined)/float64(sent)*100)
	
	// Display detailed timing breakdown
	fmt.Printf("\nDetailed Timing Breakdown:\n")
	fmt.Printf("=============================\n")
	fmt.Printf("Average Sending Time: %v\n", avgSendTime)
	fmt.Printf("Average Mining Time: %v\n", detailedAvgMiningTime)
	
	// Show individual transaction breakdowns
	fmt.Printf("\nIndividual Transaction Breakdown:\n")
	fmt.Printf("==================================\n")
	individualTimings := t.responseTracker.GetIndividualTransactionTimings()
	finalizationStatus := t.responseTracker.GetTransactionFinalizationStatus()
	
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
		
		// Show finalization status
		if finalized, exists := finalizationStatus[txHash]; exists {
			status := "Pending"
			if finalized {
				status = "Finalized"
			}
			fmt.Printf("  Status: %s\n", status)
		} else {
			fmt.Printf("  Status: Unknown\n")
		}
		
		fmt.Println()
		txIndex++
	}
	
	// Show summary
	finalizedCount := 0
	for _, finalized := range finalizationStatus {
		if finalized {
			finalizedCount++
		}
	}
	fmt.Printf("Finalization Summary: %d/%d transactions finalized (%.1f%%)\n", finalizedCount, sent, float64(finalizedCount)/float64(sent)*100)

	// Block inclusion summary (how many txs landed per block)
	// Build counts per normalized block number
	normalizeBlock := func(block string) string {
		if strings.HasPrefix(block, "x") || strings.HasPrefix(block, "X") {
			return "0x" + block[1:]
		}
		if !strings.HasPrefix(block, "0x") && !strings.HasPrefix(block, "0X") {
			return "0x" + block
		}
		return block
	}

	blockCounts := make(map[string]int)
	for _, timing := range individualTimings {
		if blockNumber, exists := timing["blockNumber"]; exists {
			b := normalizeBlock(blockNumber.(string))
			blockCounts[b]++
		}
	}

	// Sort blocks by numeric height
	type blockEntry struct { block string; num uint64; count int }
	var blocks []blockEntry
	for b, c := range blockCounts {
		bn := b
		if strings.HasPrefix(bn, "0x") || strings.HasPrefix(bn, "0X") {
			bn = bn[2:]
		}
		if len(bn)%2 != 0 { bn = "0" + bn }
		num, err := strconv.ParseUint(bn, 16, 64)
		if err != nil { num = ^uint64(0) }
		blocks = append(blocks, blockEntry{block: b, num: num, count: c})
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].num < blocks[j].num })

	fmt.Printf("\nBlock Inclusion Summary:\n")
	fmt.Printf("========================\n")
	for _, be := range blocks {
		fmt.Printf("Block %s: %d tx\n", be.block, be.count)
	}
	
	// Write final results to file
	if file != nil {
		fmt.Fprintf(file, "\nFinal Results:\n")
		fmt.Fprintf(file, "================\n")
		fmt.Fprintf(file, "Total Sent: %d\n", sent)
		fmt.Fprintf(file, "Total Mined: %d\n", mined)
		fmt.Fprintf(file, "Send Duration: %v\n", sendDuration)
		fmt.Fprintf(file, "Total Duration: %v\n", totalDuration)
		fmt.Fprintf(file, "Send TPS: %.2f\n", float64(sent)/sendDuration.Seconds())
		fmt.Fprintf(file, "Final Mining TPS: %.4f\n", finalTPS)
		fmt.Fprintf(file, "Average Mining Time: %v\n", detailedAvgMiningTime)
		fmt.Fprintf(file, "Success Rate: %.1f%%\n", float64(mined)/float64(sent)*100)
		
		// Write detailed timing breakdown to file
		fmt.Fprintf(file, "\nDetailed Timing Breakdown:\n")
		fmt.Fprintf(file, "=============================\n")
		fmt.Fprintf(file, "Average Sending Time: %v\n", avgSendTime)
		fmt.Fprintf(file, "Average Mining Time: %v\n", detailedAvgMiningTime)
		
		// Individual breakdown
		fmt.Fprintf(file, "\nIndividual Transaction Breakdown:\n")
		fmt.Fprintf(file, "==================================\n")
		ind := t.responseTracker.GetIndividualTransactionTimings()
		finalizationStatus := t.responseTracker.GetTransactionFinalizationStatus()
		txIndex := 1
		for txHash, timing := range ind {
			fmt.Fprintf(file, "Transaction %d (%s...%s):\n", txIndex, txHash[:10], txHash[len(txHash)-10:])
			if sendTime, ok := timing["sendTime"]; ok { fmt.Fprintf(file, "  Send Time: %v\n", sendTime) }
			if miningTime, ok := timing["miningTime"]; ok { fmt.Fprintf(file, "  Mining Time: %v\n", miningTime) }
			if finalizationTime, ok := timing["finalizationTime"]; ok { fmt.Fprintf(file, "  Finalization Time: %v\n", finalizationTime) }
			if blockNumber, ok := timing["blockNumber"]; ok { fmt.Fprintf(file, "  Block: %s\n", blockNumber) }
			
			// Show finalization status
			if finalized, exists := finalizationStatus[txHash]; exists {
				status := "Pending"
				if finalized {
					status = "Finalized"
				}
				fmt.Fprintf(file, "  Status: %s\n", status)
			} else {
				fmt.Fprintf(file, "  Status: Unknown\n")
			}
			
			fmt.Fprintln(file)
			txIndex++
		}
		
		// Show summary
		finalizedCount := 0
		for _, finalized := range finalizationStatus {
			if finalized {
				finalizedCount++
			}
		}
		fmt.Fprintf(file, "Finalization Summary: %d/%d transactions finalized (%.1f%%)\n", finalizedCount, sent, float64(finalizedCount)/float64(sent)*100)

		// Block inclusion summary (file)
		normalizeBlock := func(block string) string {
			if strings.HasPrefix(block, "x") || strings.HasPrefix(block, "X") {
				return "0x" + block[1:]
			}
			if !strings.HasPrefix(block, "0x") && !strings.HasPrefix(block, "0X") {
				return "0x" + block
			}
			return block
		}

		blockCounts := make(map[string]int)
		for _, timing := range ind {
			if blockNumber, exists := timing["blockNumber"]; exists {
				b := normalizeBlock(blockNumber.(string))
				blockCounts[b]++
			}
		}

		type blockEntry struct { block string; num uint64; count int }
		var blocks []blockEntry
		for b, c := range blockCounts {
			bn := b
			if strings.HasPrefix(bn, "0x") || strings.HasPrefix(bn, "0X") { bn = bn[2:] }
			if len(bn)%2 != 0 { bn = "0" + bn }
			num, err := strconv.ParseUint(bn, 16, 64)
			if err != nil { num = ^uint64(0) }
			blocks = append(blocks, blockEntry{block: b, num: num, count: c})
		}
		sort.Slice(blocks, func(i, j int) bool { return blocks[i].num < blocks[j].num })

		fmt.Fprintf(file, "\nBlock Inclusion Summary:\n")
		fmt.Fprintf(file, "========================\n")
		for _, be := range blocks {
			fmt.Fprintf(file, "Block %s: %d tx\n", be.block, be.count)
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
				if blockNumber, timestamp := t.checkTransactionInBlock(txHash, currentBlock); blockNumber != "" {
					// Calculate mining time using detection time (more reliable than block timestamp)
					miningTime := time.Since(startTime)
					fmt.Printf("Transaction %s mined in block %s (mining time %v)\n", txHash, blockNumber, miningTime)
					
					// Record mining time and block timestamp
					t.responseTracker.RecordTransactionMinedInBlockWithTime(txHash, blockNumber, miningTime)
					
					// If we have block timestamp, use it for finalization timing
					if timestamp > 0 {
						go t.monitorRealTransactionFinalizationWithBlockTime(txHash, blockNumber, timestamp)
					} else {
						go t.monitorRealTransactionFinalization(txHash, blockNumber, startTime)
					}
					
					return
				}
			}
			
			// Check if we've been waiting too long
			if time.Since(startTime) > maxWaitTime { return }
			
		case <-receiptTicker.C:
			// Check transaction receipt for immediate confirmation
			if blockNumber, timestamp := t.checkTransactionReceipt(txHash); blockNumber != "" {
				// Calculate mining time using detection time (more reliable than block timestamp)
				miningTime := time.Since(startTime)
				fmt.Printf("Transaction %s confirmed via receipt in block %s (mining time %v)\n", txHash, blockNumber, miningTime)
				
				// Record mining time
				t.responseTracker.RecordTransactionMinedInBlockWithTime(txHash, blockNumber, miningTime)
				
				// If we have block timestamp, use it for finalization timing
				if timestamp > 0 {
					go t.monitorRealTransactionFinalizationWithBlockTime(txHash, blockNumber, timestamp)
				} else {
					go t.monitorRealTransactionFinalization(txHash, blockNumber, startTime)
				}
				
				return
			}
			
			// quiet while checking
		}
	}
}

// checkTransactionInBlock checks if a transaction is included in a specific block
func (t *TransactionTPS) checkTransactionInBlock(txHash string, blockNumber string) (string, int64) {
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
		return "", 0
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0
	}
	
	responseStr := string(body)
	
	// Check if the block contains our transaction
	if strings.Contains(responseStr, txHash) {
		// Extract block timestamp
		timestampStart := strings.Index(responseStr, `"timestamp":"`) + 13
		if timestampStart > 12 {
			timestampEnd := strings.Index(responseStr[timestampStart:], `"`)
			if timestampEnd > 0 {
				timestampStr := responseStr[timestampStart : timestampStart+timestampEnd]
				// Handle hex (0x...) or decimal timestamp formats
				if strings.HasPrefix(timestampStr, "0x") || strings.HasPrefix(timestampStr, "0X") {
					if v, err := strconv.ParseUint(timestampStr[2:], 16, 64); err == nil {
						return blockNumber, int64(v)
					}
				} else {
					if v, err := strconv.ParseInt(timestampStr, 10, 64); err == nil {
						return blockNumber, v
					}
				}
			}
		}
		return blockNumber, 0
	}
	
	return "", 0
}

// checkTransactionReceipt checks transaction receipt for immediate confirmation
func (t *TransactionTPS) checkTransactionReceipt(txHash string) (string, int64) {
	// Use eth_getTransactionReceipt for immediate confirmation
	jsonData := fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_getTransactionReceipt","params":["%s"],"id":1}`, txHash)
	
	resp, err := http.Post(t.nodeURL, "application/json", strings.NewReader(jsonData))
	if err != nil {
		return "", 0
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0
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
				var formattedBlockNumber string
				if strings.HasPrefix(blockNumber, "0x") {
					// Remove 0x prefix and add x prefix for our format
					formattedBlockNumber = "x" + blockNumber[2:]
				} else {
					formattedBlockNumber = blockNumber
				}
				
				// Try to get block timestamp for accurate timing
				// We need to make another call to get the block details
				if timestamp := t.getBlockTimestamp(formattedBlockNumber); timestamp > 0 {
					return formattedBlockNumber, timestamp
				}
				
				return formattedBlockNumber, 0
			}
		}
	}
	
	return "", 0
}

// getBlockTimestamp gets the timestamp of a specific block
func (t *TransactionTPS) getBlockTimestamp(blockNumber string) int64 {
	formattedBlockNumber := blockNumber
	if strings.HasPrefix(blockNumber, "x") {
		formattedBlockNumber = "0x" + blockNumber[1:]
	} else if !strings.HasPrefix(blockNumber, "0x") {
		formattedBlockNumber = "0x" + blockNumber
	}
	
	jsonData := fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["%s",false],"id":1}`, formattedBlockNumber)
	
	resp, err := http.Post(t.nodeURL, "application/json", strings.NewReader(jsonData))
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0
	}
	
	responseStr := string(body)
	
	// Extract block timestamp
	timestampStart := strings.Index(responseStr, `"timestamp":"`) + 13
	if timestampStart > 12 {
		timestampEnd := strings.Index(responseStr[timestampStart:], `"`)
		if timestampEnd > 0 {
			timestampStr := responseStr[timestampStart : timestampStart+timestampEnd]
			// Handle hex (0x...) or decimal timestamp formats
			if strings.HasPrefix(timestampStr, "0x") || strings.HasPrefix(timestampStr, "0X") {
				if v, err := strconv.ParseUint(timestampStr[2:], 16, 64); err == nil {
					return int64(v)
				}
			} else {
				if v, err := strconv.ParseInt(timestampStr, 10, 64); err == nil {
					return v
				}
			}
		}
	}
	
	return 0
}

// monitorRealTransactionFinalizationWithBlockTime monitors a transaction for finalization using block timestamp
func (t *TransactionTPS) monitorRealTransactionFinalizationWithBlockTime(txHash string, blockNumber string, blockTimestamp int64) {
	// Start finalization monitoring timing
	t.responseTracker.StartTransactionFinalizationMonitoring(txHash)
	
	blockTime := time.Unix(blockTimestamp, 0)
	maxWaitTime := 2 * time.Minute
	startTime := time.Now()
	
	ticker := time.NewTicker(1 * time.Millisecond)
	defer ticker.Stop()
	
	// Check if the original block is accepted immediately
	if t.isBlockAccepted(blockNumber) {
		// Calculate finalization time from block timestamp to acceptance
		finalizationTime := time.Since(blockTime)
		if finalizationTime < 0 {
			finalizationTime = time.Since(startTime) // Fallback if negative
		}
		fmt.Printf("Transaction %s finalized in block %s (finality %v)\n", txHash, blockNumber, finalizationTime)
		
		// Record finalization time for this transaction and all others in the same block
		t.responseTracker.RecordTransactionFinalized(txHash, finalizationTime)
		t.responseTracker.FinalizeAllTransactionsInBlock(blockNumber, finalizationTime)
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
			if time.Since(startTime) > maxWaitTime { 
				// Mark as finalized even if timeout (block is likely accepted)
				finalizationTime := time.Since(blockTime)
				if finalizationTime < 0 {
					finalizationTime = time.Since(startTime)
				}
				t.responseTracker.RecordTransactionFinalized(txHash, finalizationTime)
				return 
			}
			
			// Check if the original block is now accepted
			if t.isBlockAccepted(blockNumber) {
				finalizationTime := time.Since(blockTime)
				if finalizationTime < 0 {
					finalizationTime = time.Since(startTime)
				}
				fmt.Printf("Transaction %s finalized in block %s (finality %v)\n", txHash, blockNumber, finalizationTime)
				
				t.responseTracker.RecordTransactionFinalized(txHash, finalizationTime)
				t.responseTracker.FinalizeAllTransactionsInBlock(blockNumber, finalizationTime)
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

	// fallback finalization - mark as finalized if we've been monitoring this long
	finalizationTime := time.Since(blockTime)
	if finalizationTime < 0 {
		finalizationTime = time.Since(startTime)
	}
	t.responseTracker.RecordTransactionFinalized(txHash, finalizationTime)
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
		
		// Record finalization time for this transaction and all others in the same block
		t.responseTracker.RecordTransactionFinalized(txHash, finalizationTime)
		t.responseTracker.FinalizeAllTransactionsInBlock(blockNumber, finalizationTime)
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
			if time.Since(startTime) > maxWaitTime { 
				// Mark as finalized even if timeout (block is likely accepted)
				t.responseTracker.RecordTransactionFinalized(txHash, time.Since(startTime))
				return 
			}
			
			// Check if the original block is now accepted
			if t.isBlockAccepted(blockNumber) {
				finalizationTime := time.Since(startTime)
				fmt.Printf("Transaction %s finalized in block %s (finality %v)\n", txHash, blockNumber, finalizationTime)
				
				t.responseTracker.RecordTransactionFinalized(txHash, finalizationTime)
				t.responseTracker.FinalizeAllTransactionsInBlock(blockNumber, finalizationTime)
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

	// fallback finalization - mark as finalized if we've been monitoring this long
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
