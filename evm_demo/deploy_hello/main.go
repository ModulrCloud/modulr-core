package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Minimal "hello world" contract in raw EVM bytecode (no Solidity needed).
//
// Semantics:
// - storage[0] holds a bytes32 value (default = "hello world" padded with zeros).
// - If calldata size == 0 -> return storage[0] as 32 bytes.
// - Else -> SSTORE(0, CALLDATALOAD(0)) and STOP.
//
// Read:  eth_call with data "0x" (empty)
// Write: send tx with data "0x" + 32 bytes (hex) of new value
//
// Runtime:
//
//	36            CALLDATASIZE
//	15            ISZERO
//	60 0c         PUSH1 0x0c
//	57            JUMPI
//	60 00         PUSH1 0x00
//	35            CALLDATALOAD
//	60 00         PUSH1 0x00
//	55            SSTORE
//	00            STOP
//	5b            JUMPDEST
//	60 00         PUSH1 0x00
//	54            SLOAD
//	60 00         PUSH1 0x00
//	52            MSTORE
//	60 20         PUSH1 0x20
//	60 00         PUSH1 0x00
//	f3            RETURN
const runtimeHex = "3615600c57600035600055005b60005460005260206000f3"

// Init code:
//
//	PUSH32 <hello_world_bytes32>
//	PUSH1  0x00
//	SSTORE
//	PUSH1  runtimeLen(0x18)
//	PUSH1  runtimeOffset(0x30)
//	PUSH1  0x00
//	CODECOPY
//	PUSH1  runtimeLen(0x18)
//	PUSH1  0x00
//	RETURN
//	<runtime bytes>
//
// runtimeOffset = 0x30 because init prefix is 48 bytes long.
const initPrefixHex = "7f" + helloWorldBytes32Hex + "6000556018603060003960186000f3"

// "hello world" padded to bytes32 (32 bytes / 64 hex chars).
const helloWorldBytes32Hex = "68656c6c6f20776f726c64000000000000000000000000000000000000000000"

func main() {
	rpcURL := getenv("RPC_URL", "http://localhost:7332/evm_rpc")
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
	gasLimit := uint64(500_000)           // plenty for this tiny contract

	initCode := mustHex(initPrefixHex + runtimeHex)

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       nil, // contract creation
		Value:    big.NewInt(0),
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Data:     initCode,
	})

	signer := types.LatestSignerForChainID(chainID)
	signed, err := types.SignTx(tx, signer, key)
	if err != nil {
		log.Fatalf("sign tx: %v", err)
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		log.Fatalf("send tx: %v", err)
	}
	fmt.Printf("Deploy tx sent: %s\n", signed.Hash().Hex())

	receipt := waitReceipt(client, signed.Hash(), 60*time.Second)
	fmt.Printf("Mined in block: %s\n", receipt.BlockNumber.String())
	fmt.Printf("Contract address: %s\n", receipt.ContractAddress.Hex())

	// Convenience: print initial value via eth_call.
	val := callRead(client, receipt.ContractAddress)
	fmt.Printf("Initial value: %q\n", val)
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

func callRead(client *ethclient.Client, contract common.Address) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
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
	return strings.TrimRight(string(out[:32]), "\x00")
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

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
