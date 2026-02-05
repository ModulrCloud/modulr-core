# ModulrCore HTTP API

This document describes every HTTP endpoint registered in [`server.go`](../http_pack/server.go), the expected request inputs, and the response formats returned by the node.

## Live statistics

### `GET /last_height`
Returns the latest executed block height tracked by the node.

- **Success (200)**: `{ "lastHeight": <int> }`.
- **Errors**
  - `404` — the node has not executed any blocks yet.
  - `500` — failed to marshal the response.

**Example request**
```bash
curl https://localhost:7332/last_height
```

**Example response**
```json
{ "lastHeight": 1024 }
```

### `GET /live_stats`
Returns a snapshot of the node's current view of the chain along with runtime parameters.

- **Success (200)**: JSON object with the following shape:
  - `statistics`: Aggregated chain metrics.
    - `lastHeight`: Absolute height of the latest executed block (starts at `-1` before any block is executed).
    - `lastBlockHash`: Hash of the latest executed block.
    - `totalTransactions`: Total number of transactions observed in executed blocks.
    - `successfulTransactions`: Transactions that passed validation and were applied.
    - `failedTransactions`: Transactions that failed validation.
    - `totalFees`: Total amount of fees collected across executed blocks.
  - `networkParameters`: Current [`structures.NetworkParameters`](../structures/network_parameters.go).
  - `epoch`: Current [`structures.EpochDataHandler`](../structures/epoch.go).
- **Errors**
  - `500` — failed to marshal response.

**Example request**
```bash
curl https://localhost:7332/live_stats
```

**Example response**
```json
{
  "statistics": {
    "lastHeight": 1024,
    "lastBlockHash": "00f9...",
    "totalTransactions": 18230,
    "successfulTransactions": 17980,
    "failedTransactions": 250,
    "totalFees": 941000
  },
  "networkParameters": {
    "epochDuration": 60000,
    "quorumSize": 5,
    "minimalStakePerStaker": 1000000,
    "validatorRequiredStake": 5000000,
    "leadersCount": 12
  },
  "epoch": {
    "id": 42,
    "hash": "2ad1...",
    "validatorsRegistry": ["validator_1", "validator_2"],
    "startTimestamp": 1714042385123,
    "quorum": ["validator_1", "validator_3", "validator_7", "validator_8", "validator_12"],
    "leadersSequence": ["validator_1", "validator_3", "validator_7"],
    "currentLeaderIndex": 1
  }
}
```

## Blocks

### `GET /block/{id}`
Retrieves a block by its unique identifier.

- **Path parameters**
  - `id`: Block identifier, e.g. `<epochIndex>:<leaderPublicKey>:<blockIndex>`.
- **Success (200)**: JSON serialization of [`block_pack.Block`](../block_pack/block.go).
- **Errors**
  - `400` — parameter is missing or not a string.
  - `404` — block is not found.

**Example request**
```bash
curl https://localhost:7332/block/0:9GQ46rqY238rk2neSwgidap9ww5zbAN4dyqyC7j5ZnBK:30
```

**Example response**
```json
{
  "creator": "9GQ46rqY238rk2neSwgidap9ww5zbAN4dyqyC7j5ZnBK",
  "time": 1714042385123,
  "epoch": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef#0",
  "transactions": [
    {
      "v": 1,
      "type": "transfer",
      "from": "ed25519_sender...",
      "to": "ed25519_receiver...",
      "amount": 125000000,
      "fee": 1000,
      "sig": "7c1a...",
      "nonce": 56,
      "payload": {
        "memo": "Payout"
      }
    }
  ],
  "extraData": {
    "rest": {},
    "delayedTxsBatch": {
      "epochIndex": 42,
      "delayedTransactions": [],
      "proofs": {}
    }
  },
  "index": 0,
  "prevHash": "00f9...",
  "sig": "4d2e..."
}
```

### `GET /height/{absoluteHeightIndex}`
Loads a block by its absolute height.

- **Path parameters**
  - `absoluteHeightIndex`: Block height as a base-10 string.
- **Success (200)**: Same response body as `GET /block/{id}`.
- **Errors**
  - `400` — parameter is empty or not a valid integer.
  - `404` — the block height is unknown.
  - `500` — internal error while reading from storage.

**Example request**
```bash
curl https://localhost:7332/height/1024
```

**Example response**
```json
{
  "creator": "9GQ46rqY238rk2neSwgidap9ww5zbAN4dyqyC7j5ZnBK",
  "time": 1714042385123,
  "epoch": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef#0",
  "transactions": [
    {
      "v": 1,
      "type": "transfer",
      "from": "ed25519_sender...",
      "to": "ed25519_receiver...",
      "amount": 125000000,
      "fee": 1000,
      "sig": "7c1a...",
      "nonce": 56,
      "payload": {
        "memo": "Payout"
      }
    }
  ],
  "extraData": {
    "rest": {},
    "delayedTxsBatch": {
      "epochIndex": 42,
      "delayedTransactions": [],
      "proofs": {}
    }
  },
  "index": 0,
  "prevHash": "00f9...",
  "sig": "4d2e..."
}
```

### `GET /aggregated_finalization_proof/{blockId}`
Returns the aggregated finalization proof for a block.

- **Path parameters**
  - `blockId`: Identifier of the block whose proof should be returned.
- **Success (200)**: [`structures.AggregatedFinalizationProof`](../structures/proofs.go).
- **Errors**
  - `400` — invalid parameter value.
  - `404` — proof not found.

**Example request**
```bash
curl https://localhost:7332/aggregated_finalization_proof/0:9GQ46rqY238rk2neSwgidap9ww5zbAN4dyqyC7j5ZnBK:30
```

**Example response**
```json
{
  "prevBlockHash": "ddaa...",
  "blockId": "42:ed25519_abcd...:0",
  "blockHash": "f7c0...",
  "proofs": {
    "ed25519_validator_1": "f31b...",
    "ed25519_validator_8": "a912..."
  }
}
```

### `GET /transaction/{hash}`
Finds a transaction by its hash.

- **Path parameters**
  - `hash`: Hex-encoded transaction hash.
- **Success (200)**: Object containing both the transaction and its receipt:
  - `tx`: [`structures.Transaction`](../structures/transaction.go).
  - `receipt`: [`structures.TransactionReceipt`](../structures/transaction.go).
    - `reason`: Empty string when `success=true`, otherwise contains the failure reason.
- **Errors**
  - `400` — missing or invalid hash.
  - `404` — transaction does not exist.
  - `500` — storage read or JSON parse error.

**Example request**
```bash
curl https://localhost:7332/transaction/ab5f5cb2...
```

**Example response**
```json
{
  "tx": {
    "v": 1,
    "from": "ed25519_sender...",
    "to": "ed25519_receiver...",
    "amount": 50000000,
    "fee": 1000,
    "sig": "aabbcc...",
    "nonce": 57,
    "payload": {
      "memo": "Invoice #582"
    }
  },
  "receipt": {
    "block": "42:ed25519_abcd...:0",
    "position": 3,
    "success": true,
    "reason": ""
  }
}
```

### `POST /transaction`
Accepts a transaction into the mempool or forwards it to the current leader.

- **Request body**: [`structures.Transaction`](../structures/transaction.go). Required fields are `from`, `nonce`, and `sig`; the remaining fields are validated by the node.
- **Success (200)**
  - `{"status":"OK"}` — transaction enqueued locally.
  - `{"status":"Ok, tx redirected to current leader"}` — forwarded when this node is not the leader.
- **Errors**
  - `400` — invalid JSON or missing required fields (`{"err":"Invalid JSON"}` / `{"err":"Event structure is wrong"}`).
  - `429` — mempool is full (`{"err":"Mempool is fullfilled"}`).
  - `500` — failed to forward the request to the leader (`{"err":"Impossible to redirect to current leader"}`).

**Example request**
```bash
curl \
  -X POST https://localhost:7332/transaction \
  -H 'Content-Type: application/json' \
  -d '{
        "v": 1,
        "type": "transfer",
        "from": "ed25519_sender...",
        "to": "ed25519_receiver...",
        "amount": 50000000,
        "fee": 1000,
        "sig": "aabbcc...",
        "nonce": 57,
        "payload": {"memo": "Invoice #582"}
      }'
```

**Example success response**
```json
{
  "status": "OK"
}
```

**Example error response**
```json
{
  "err": "Mempool is fullfilled"
}
```

## Accounts

### `GET /account/{accountId}`
Fetches account state from the LevelDB-backed state store.

- **Path parameters**
  - `accountId`: Account identifier (public key string).
- **Success (200)**: [`structures.Account`](../structures/account.go) with `balance`, `nonce`, `initiatedTransactions`, and `successfulInitiatedTransactions` counters for the sender's activity.
- **Errors**
  - `400` — invalid account identifier.
  - `404` — account not found.
  - `500` — storage read or JSON parse error.

**Example request**
```bash
curl https://localhost:7332/account/6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE
```

**Example response**
```json
{
  "balance": 275000000,
  "nonce": 12,
  "initiatedTransactions": 34,
  "successfulInitiatedTransactions": 30
}
```

## Validators

### `GET /validator/{validatorPubkey}`
Fetches validator storage (staking and metadata) from the state store.

- **Path parameters**
  - `validatorPubkey`: Validator public key string.
- **Success (200)**: [`structures.ValidatorStorage`](../structures/genesis.go).
- **Errors**
  - `400` — invalid validator pubkey.
  - `404` — validator not found.
  - `500` — storage read or JSON parse error.

**Example request**
```bash
curl https://localhost:7332/validator/6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE
```

**Example response**
```json
{
  "pubkey": "6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE",
  "percentage": 10,
  "totalStaked": 1500000000,
  "stakers": {
    "staker_1_pubkey": 1000000000,
    "staker_2_pubkey": 500000000
  },
  "validatorURL": "https://validator.example.com",
  "wssValidatorURL": "wss://validator.example.com/ws"
}
```

## Epoch data

### `GET /epoch_data/{epochIndex}`
Returns serialized epoch snapshot stored under `EPOCH_HANDLER:{epochIndex}`.

- **Path parameters**
  - `epochIndex`: Epoch number as a string.
- **Success (200)**: Raw JSON payload for the epoch snapshot: the latest `EpochDataHandler` persisted for the epoch plus the `networkParameters` used during that epoch.
- **Errors**
  - `400` — invalid epoch index.
  - `404` — no data for the requested epoch.

**Example request**
```bash
curl https://localhost:7332/epoch_data/42
```

**Example response**
```json
{
  "id": 42,
  "hash": "9f8c6d",
  "startTimestamp": 1714042385123,
  "currentLeaderIndex": 3,
  "leadersSequence": [
    "ed25519_leader_0",
    "ed25519_leader_1",
    "ed25519_leader_2",
    "ed25519_leader_3"
  ],
  "quorum": [
    "ed25519_validator_0",
    "ed25519_validator_5",
    "ed25519_validator_8",
    "ed25519_validator_9",
    "ed25519_validator_12"
  ],
  "validatorsRegistry": [
    "ed25519_validator_0",
    "ed25519_validator_1",
    "ed25519_validator_2"
  ],
  "networkParameters": {
    "epochDuration": 60000,
    "quorumSize": 5,
    "minimalStakePerStaker": 1000000,
    "validatorRequiredStake": 5000000,
    "leadersCount": 12
  }
}
```