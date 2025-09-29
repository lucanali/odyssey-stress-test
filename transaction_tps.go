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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// TransactionTPS handles transaction throughput testing
type TransactionTPS struct {
	nodeURL         string
	privateKey      *ecdsa.PrivateKey
	iterations      int
	responseTracker *ResponseTimeTracker
	outputFilePath  string
	fileWriteMu     sync.Mutex
	finalizedLogged map[string]bool
}

// NewTransactionTPS creates a new TransactionTPS instance
func NewTransactionTPS(nodeURL string, privateKey *ecdsa.PrivateKey, iterations int, responseTracker *ResponseTimeTracker) *TransactionTPS {
	return &TransactionTPS{
		nodeURL:         nodeURL,
		privateKey:      privateKey,
		iterations:      iterations,
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

			// Record transaction sent
			t.responseTracker.RecordTransactionSent(txHash)

			fmt.Printf("Transaction %d sent: %s\n", txNonce, txHash)

			// Send transaction hash to channel
			txChan <- txHash

			// Start monitoring immediately after sending
			go t.monitorTransactionInclusion(txHash)

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

	// Monitor until all transactions are included in blocks
	fmt.Println("Monitoring transaction inclusion until ALL transactions are in blocks...")
	fmt.Println("This may take several minutes depending on network conditions...")

	// Create output file for detailed results
	outputFile := fmt.Sprintf("transaction_tps_results_%s.txt", time.Now().Format("20060102_150405"))
	file, err := os.Create(outputFile)
	if err != nil {
		fmt.Printf("Warning: Could not create output file: %v\n", err)
	} else {
		defer file.Close()
		// store for incremental appends
		t.outputFilePath = outputFile
		if t.finalizedLogged == nil {
			t.finalizedLogged = make(map[string]bool)
		}
		fmt.Fprintf(file, "Odyssey Chain Transaction TPS Test Results\n")
		fmt.Fprintf(file, "==========================================\n")
		fmt.Fprintf(file, "Test Date: %s\n", time.Now().Format("2006-01-02 15:04:05"))
		fmt.Fprintf(file, "Node URL: %s\n", t.nodeURL)
		fmt.Fprintf(file, "Chain ID: 131313\n")
		fmt.Fprintf(file, "Total Transactions: %d\n", sentCount)
		fmt.Fprintf(file, "Start Time: %s\n", startTime.Format("15:04:05"))
		fmt.Fprintf(file, "Send Duration: %v\n", sendDuration)
		fmt.Fprintf(file, "\nBlock Inclusion Progress:\n")
		fmt.Fprintf(file, "==========================\n")
		fmt.Fprintf(file, "\nFinalized Transactions (appended as they complete):\n")
		fmt.Fprintf(file, "===============================================\n")
	}

	// Monitor until all transactions are included in blocks
	monitorTicker := time.NewTicker(1 * time.Millisecond)
	defer monitorTicker.Stop()

	for {
		select {
		case <-monitorTicker.C:
			sent, mined, _, _ := t.responseTracker.GetTransactionStats()

			// Check if all transactions are included in blocks
			if mined >= sent {
				fmt.Println("\n ALL TRANSACTIONS INCLUDED IN BLOCKS! Test completed successfully!")
				goto done
			}
		}
	}

done:
	// Final statistics
	sent, mined, _, _ := t.responseTracker.GetTransactionStats()

	// Calculate final TPS
	totalDuration := time.Since(startTime)
	finalTPS := float64(mined) / totalDuration.Seconds()

	fmt.Printf("\nFinal Transaction TPS Test Results:\n")
	fmt.Printf("=====================================\n")
	fmt.Printf("Total Sent: %d\n", sent)
	fmt.Printf("Total Included in Blocks: %d\n", mined)
	fmt.Printf("Send Duration: %v\n", sendDuration)
	fmt.Printf("Total Duration: %v\n", totalDuration)
	fmt.Printf("Final TPS: %.4f\n", finalTPS)
	fmt.Printf("Success Rate: %.1f%%\n", float64(mined)/float64(sent)*100)

	// Show individual transaction breakdowns
	fmt.Printf("\nIndividual Transaction Breakdown:\n")
	fmt.Printf("==================================\n")
	individualTimings := t.responseTracker.GetIndividualTransactionTimings()

	txIndex := 1
	for txHash, timing := range individualTimings {
		fmt.Printf("Transaction %d (%s...%s):\n", txIndex, txHash[:10], txHash[len(txHash)-10:])

		if blockNumber, exists := timing["blockNumber"]; exists {
			fmt.Printf("  Block: %s\n", blockNumber)
		}

		if finalizationTime, exists := timing["finalizationTime"]; exists {
			fmt.Printf("  Block Timestamp: %v\n", finalizationTime)
		}

		fmt.Println()
		txIndex++
	}

	// Block inclusion summary (how many txs landed per block)
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
	type blockEntry struct {
		block string
		num   uint64
		count int
	}
	var blocks []blockEntry
	for b, c := range blockCounts {
		bn := b
		if strings.HasPrefix(bn, "0x") || strings.HasPrefix(bn, "0X") {
			bn = bn[2:]
		}
		if len(bn)%2 != 0 {
			bn = "0" + bn
		}
		num, err := strconv.ParseUint(bn, 16, 64)
		if err != nil {
			num = ^uint64(0)
		}
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
		fmt.Fprintf(file, "Total Included in Blocks: %d\n", mined)
		fmt.Fprintf(file, "Send Duration: %v\n", sendDuration)
		fmt.Fprintf(file, "Total Duration: %v\n", totalDuration)
		fmt.Fprintf(file, "Final TPS: %.4f\n", finalTPS)
		fmt.Fprintf(file, "Success Rate: %.1f%%\n", float64(mined)/float64(sent)*100)

		// Individual breakdown
		fmt.Fprintf(file, "\nIndividual Transaction Breakdown:\n")
		fmt.Fprintf(file, "==================================\n")
		ind := t.responseTracker.GetIndividualTransactionTimings()
		txIndex := 1
		for txHash, timing := range ind {
			fmt.Fprintf(file, "Transaction %d (%s...%s):\n", txIndex, txHash[:10], txHash[len(txHash)-10:])
			if blockNumber, ok := timing["blockNumber"]; ok {
				fmt.Fprintf(file, "  Block: %s\n", blockNumber)
			}
			if finalizationTime, ok := timing["finalizationTime"]; ok {
				fmt.Fprintf(file, "  Block Timestamp: %v\n", finalizationTime)
			}
			fmt.Fprintln(file)
			txIndex++
		}

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

		type blockEntry struct {
			block string
			num   uint64
			count int
		}
		var blocks []blockEntry
		for b, c := range blockCounts {
			bn := b
			if strings.HasPrefix(bn, "0x") || strings.HasPrefix(bn, "0X") {
				bn = bn[2:]
			}
			if len(bn)%2 != 0 {
				bn = "0" + bn
			}
			num, err := strconv.ParseUint(bn, 16, 64)
			if err != nil {
				num = ^uint64(0)
			}
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
		nonce,         // nonce
		fromAddress,   // to (self)
		big.NewInt(0), // value (0 ETH)
		21000,         // gas limit
		gasPrice,      // gas price
		nil,           // data
	)

	// Sign transaction with hardcoded chain ID (131313 for local testnet)
	// chainID, err := t.getChainID()
	// if err != nil {
	// 	return "", fmt.Errorf("failed to get chain ID: %v", err)
	// }
	signer := types.NewEIP155Signer(big.NewInt(43112))
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

// getChainID gets the chain ID from the network
func (t *TransactionTPS) getChainID() (*big.Int, error) {
	jsonData := `{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}`

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

	// Parse hex chain ID
	if strings.HasPrefix(result.Result, "0x") {
		hexStr := result.Result[2:] // Remove "0x" prefix
		// Pad with leading zero if odd length
		if len(hexStr)%2 != 0 {
			hexStr = "0" + hexStr
		}
		chainID, err := strconv.ParseUint(hexStr, 16, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse hex chain ID: %v", err)
		}
		return big.NewInt(int64(chainID)), nil
	}

	return nil, fmt.Errorf("invalid hex string: %s", result.Result)
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

// monitorTransactionInclusion monitors a transaction until it's included in a block
func (t *TransactionTPS) monitorTransactionInclusion(txHash string) {
	maxWaitTime := 30 * time.Minute
	startTime := time.Now()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Check if we've been waiting too long
			if time.Since(startTime) > maxWaitTime {
				fmt.Printf("Transaction %s timed out waiting for inclusion\n", txHash)
				return
			}

			// Check transaction receipt for immediate confirmation
			if blockNumber, timestamp := t.checkTransactionReceipt(txHash); blockNumber != "" {
				fmt.Printf("Transaction %s included in block %s\n", txHash, blockNumber)

				// Record transaction as mined with block info
				t.responseTracker.RecordTransactionMinedInBlock(txHash, blockNumber)

				// Record finalization time as block timestamp
				if timestamp > 0 {
					blockTime := time.Unix(timestamp, 0)
					// Store block timestamp as finalization time
					t.responseTracker.RecordTransactionFinalized(txHash, blockTime)
					// Also record block finalization with timestamp
					t.responseTracker.RecordBlockFinalized(blockNumber, blockTime)
					t.logFinalizedTx(txHash)
				}

				return
			}
		}
	}
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

// logFinalizedTx appends a single finalized transaction entry to the results file (once)
func (t *TransactionTPS) logFinalizedTx(txHash string) {
	if t.outputFilePath == "" {
		return
	}

	// prevent duplicate entries
	t.fileWriteMu.Lock()
	if t.finalizedLogged == nil {
		t.finalizedLogged = make(map[string]bool)
	}
	if t.finalizedLogged[txHash] {
		t.fileWriteMu.Unlock()
		return
	}
	t.fileWriteMu.Unlock()

	// fetch timing info for this tx
	ind := t.responseTracker.GetIndividualTransactionTimings()
	timing, exists := ind[txHash]

	// open file for append
	f, err := os.OpenFile(t.outputFilePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	// prepare fields
	shortHash := txHash
	if len(txHash) > 20 {
		shortHash = txHash[:10] + "..." + txHash[len(txHash)-10:]
	}
	var blockStr string
	if exists {
		if bn, ok := timing["blockNumber"].(string); ok {
			blockStr = bn
		}
	}
	var finTimeStr string
	if exists {
		if ft, ok := timing["finalizationTime"].(time.Duration); ok {
			finTimeStr = ft.String()
		}
	}
	var miningTimeStr string
	if exists {
		if mt, ok := timing["miningTime"].(time.Duration); ok {
			miningTimeStr = mt.String()
		}
	}

	// write entry
	if blockStr != "" || finTimeStr != "" || miningTimeStr != "" {
		fmt.Fprintf(f, "Finalized: %s", shortHash)
		if blockStr != "" {
			fmt.Fprintf(f, " | Block: %s", blockStr)
		}
		if miningTimeStr != "" {
			fmt.Fprintf(f, " | Mining: %s", miningTimeStr)
		}
		if finTimeStr != "" {
			fmt.Fprintf(f, " | Finality: %s", finTimeStr)
		}
		fmt.Fprintln(f)
	} else {
		fmt.Fprintf(f, "Finalized: %s\n", shortHash)
	}

	// mark as logged
	t.fileWriteMu.Lock()
	t.finalizedLogged[txHash] = true
	t.fileWriteMu.Unlock()
}
