# Modulr EVM demos (deploy / read / write)

This directory contains 3 small Go programs that interact with Modulr's embedded EVM via **Ethereum JSON-RPC** (`/evm_rpc`).

## Prerequisites

- A running `modulr-core` node with the EVM RPC endpoint enabled:
  - default RPC URL used by demos: `http://localhost:7332/evm_rpc`
- Your EVM account must have funds (for gas).
  - Recommended: add your address to `EVM_ALLOC` in the genesis template you use (balance is **decimal wei**).
- Go installed (use the same Go version as the repo / `modulr-core` module).

## Environment variables

- **`RPC_URL`** (optional): EVM JSON-RPC URL.
  - default: `http://localhost:7332/evm_rpc`
- **`EVM_PRIVATE_KEY`** (required for deploy + write): hex secp256k1 private key (with or without `0x`).
- **`HELLO_CONTRACT`** (optional): deployed contract address (used by `read_hello` / `write_hello` if `-contract` is not provided).

## What contract is deployed?

The demo deploys a tiny contract written as raw EVM bytecode (no Solidity):

- `storage[0]` stores a **bytes32** value
- **Read**: `eth_call` with empty calldata returns `storage[0]`
- **Write**: sending a transaction with 32 bytes calldata sets `storage[0]`

So you can store up to 32 bytes (ASCII recommended) and read it back.

## Run: deploy

Deploys the contract and prints:
- tx hash
- mined block number
- contract address
- initial stored value

```bash
cd modulr-core

export RPC_URL="http://localhost:7332/evm_rpc"
export EVM_PRIVATE_KEY="0xYOUR_PRIVATE_KEY"

go run ./evm_demo/deploy_hello
```

Copy the printed contract address for the next steps, or export it:

```bash
export HELLO_CONTRACT="0xCONTRACT_ADDRESS"
```

## Run: read

Reads the current value (bytes32) and prints it as a string (zero-bytes trimmed).

```bash
cd modulr-core

export RPC_URL="http://localhost:7332/evm_rpc"
export HELLO_CONTRACT="0xCONTRACT_ADDRESS"

go run ./evm_demo/read_hello
```

Or pass the address explicitly:

```bash
go run ./evm_demo/read_hello -contract 0xCONTRACT_ADDRESS
```

## Run: write

Writes a new value (up to 32 bytes) by sending a transaction.

```bash
cd modulr-core

export RPC_URL="http://localhost:7332/evm_rpc"
export EVM_PRIVATE_KEY="0xYOUR_PRIVATE_KEY"
export HELLO_CONTRACT="0xCONTRACT_ADDRESS"

go run ./evm_demo/write_hello -msg "hello from modulr"
```

Or pass the contract explicitly:

```bash
go run ./evm_demo/write_hello -contract 0xCONTRACT_ADDRESS -msg "new value"
```

## Quick RPC sanity checks (optional)

Check chain id:

```bash
curl -s "$RPC_URL" \
  -H 'content-type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}'
```

Check balance (replace address):

```bash
curl -s "$RPC_URL" \
  -H 'content-type: application/json' \
  --data '{"jsonrpc":"2.0","id":2,"method":"eth_getBalance","params":["0xYOUR_ADDRESS","latest"]}'
```

