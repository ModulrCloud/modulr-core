package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	var contractStr string
	flag.StringVar(&contractStr, "contract", "", "contract address (0x...)")
	flag.Parse()

	rpcURL := getenv("RPC_URL", "http://localhost:7332/evm_rpc")
	if contractStr == "" {
		contractStr = strings.TrimSpace(os.Getenv("HELLO_CONTRACT"))
	}
	if contractStr == "" {
		log.Fatal("provide -contract 0x... or set HELLO_CONTRACT env var")
	}
	contract := common.HexToAddress(contractStr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		log.Fatalf("dial rpc: %v", err)
	}

	out, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &contract,
		Data: []byte{}, // empty calldata => read
		Gas:  200_000,
	}, nil)
	if err != nil {
		log.Fatalf("eth_call failed: %v", err)
	}
	if len(out) < 32 {
		log.Fatalf("unexpected eth_call output len=%d: 0x%s", len(out), hex.EncodeToString(out))
	}
	fmt.Println(strings.TrimRight(string(out[:32]), "\x00"))
}

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
