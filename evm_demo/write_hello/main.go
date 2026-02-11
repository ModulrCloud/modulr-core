package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	var contractStr string
	var msg string
	flag.StringVar(&contractStr, "contract", "", "contract address (0x...)")
	flag.StringVar(&msg, "msg", "hello from modulr", "message (<=32 bytes ASCII recommended)")
	flag.Parse()

	rpcURL := getenv("RPC_URL", "http://localhost:7332/evm_rpc")
	if contractStr == "" {
		contractStr = strings.TrimSpace(os.Getenv("HELLO_CONTRACT"))
	}
	if contractStr == "" {
		log.Fatal("provide -contract 0x... or set HELLO_CONTRACT env var")
	}
	contract := common.HexToAddress(contractStr)

	privHex := strings.TrimSpace(os.Getenv("EVM_PRIVATE_KEY"))
	if privHex == "" {
		log.Fatal("set EVM_PRIVATE_KEY to a hex secp256k1 private key (0x... or without 0x)")
	}
	privHex = strings.TrimPrefix(privHex, "0x")
	key, err := crypto.HexToECDSA(privHex)
	if err != nil {
		log.Fatalf("bad EVM_PRIVATE_KEY: %v", err)
	}
	from := crypto.PubkeyToAddress(key.PublicKey)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		log.Fatalf("dial rpc: %v", err)
	}

	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		log.Fatalf("get nonce: %v", err)
	}

	chainID := big.NewInt(7338)
	gasPrice := big.NewInt(1_000_000_000) // 1 gwei
	gasLimit := uint64(80_000)            // plenty for SSTORE

	data := encodeBytes32(msg)
	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &contract,
		Value:    big.NewInt(0),
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Data:     data,
	})

	signer := types.LatestSignerForChainID(chainID)
	signed, err := types.SignTx(tx, signer, key)
	if err != nil {
		log.Fatalf("sign tx: %v", err)
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		log.Fatalf("send tx: %v", err)
	}
	fmt.Printf("Write tx sent: %s\n", signed.Hash().Hex())

	receipt := waitReceipt(client, signed.Hash(), 60*time.Second)
	fmt.Printf("Mined in block: %s\n", receipt.BlockNumber.String())
	fmt.Printf("Status: %d, GasUsed: %d\n", receipt.Status, receipt.GasUsed)
}

func encodeBytes32(s string) []byte {
	b := []byte(s)
	if len(b) > 32 {
		b = b[:32]
	}
	out := make([]byte, 32)
	copy(out, b)
	return out
}

func waitReceipt(client *ethclient.Client, txHash common.Hash, timeout time.Duration) *types.Receipt {
	deadline := time.Now().Add(timeout)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		receipt, err := client.TransactionReceipt(ctx, txHash)
		cancel()
		if err == nil && receipt != nil {
			return receipt
		}
		if time.Now().After(deadline) {
			log.Fatalf("timeout waiting for receipt: %s (last err: %v)", txHash.Hex(), err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func mustHex(hexStr string) []byte {
	hexStr = strings.TrimSpace(hexStr)
	hexStr = strings.TrimPrefix(hexStr, "0x")
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		log.Fatalf("bad hex: %v", err)
	}
	return b
}

