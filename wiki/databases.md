# Databases Overview

All databases are LevelDB instances, declared in `databases/databases.go`.

---

## 1. BLOCKS

Raw block storage and generation thread metadata.

| Key | Value | Written by | Notes |
|-----|-------|-----------|-------|
| `GT` | `GenerationThreadMetadataHandler` JSON | `block_generation.go` | Generation thread metadata (nextIndex, prevHash, epoch) |
| `{epochId}:{creator}:{index}` | Block JSON (`block_pack.Block`) | `websocket_pack/routes.go` (on `get_finalization_proof`), `block_generation.go` | Raw block data, keyed by blockId |

**Recovery**: Can be wiped. Blocks are available from PoD and other validators.

---

## 2. STATE

Permanent chain state — accounts, validators, transaction receipts, block index, epoch statistics, delayed transactions, and execution thread metadata.

| Key | Value | Written by | Notes |
|-----|-------|-----------|-------|
| `CHAIN_CURSOR` | `ChainCursor` JSON | `block_execution.go`, `entrypoint.go` | The single source of truth for execution-thread state: offsets (HeightOffset, EpochOffset), NetworkId, CoreMajorVersion, NetworkParameters, EpochDataHandler, Statistics, EpochStatistics |
| `BLOCK_INDEX:{height}` | `blockId` string | `block_execution.go` | Absolute height → blockId mapping |
| `TX:{hash}` | Transaction receipt JSON (block location) | `block_execution.go` | Transaction receipt / location for explorers and SDK |
| `EPOCH_STATS:{epochId}` | `EpochStatistics` JSON | `block_execution.go` (`setupNextEpochFromRotationProof`) | Per-epoch statistics snapshot, written at epoch boundary using absolute epoch id |
| `EPOCH_DATA:{epochId}` | `EpochDataSnapshot` JSON | `block_execution.go`, `entrypoint.go` | Durable epoch snapshot for API/Explorer, keyed by absolute epoch id |
| `VALIDATOR_STORAGE:{pubkey}` | Validator JSON | `block_execution.go` (via `persistTouchedState`) | Validator data (stake, metadata) |
| `{accountPubkey}` | Account JSON (balance, nonce, etc.) | `block_execution.go` (via `persistTouchedState`) | Account state |
| `DELAYED_TRANSACTIONS:{epochId}` | `[]map[string]string` JSON | `block_execution.go` (`addDelayedTransactionsToBatch`) | Delayed tx payloads queued for `epochId` (actually targets epoch+2) |

**Recovery**: **Preserved**. STATE holds the permanent world state. On a network restart, the operator wipes `CHAIN_CURSOR.EpochDataHandler` (its `Hash == ""` is the genesis-init sentinel) while keeping `Statistics`, `NetworkParameters`, `HeightOffset`, `EpochOffset`, accounts and validators intact.

---

## 3. EPOCH_DATA

Ephemeral per-epoch data: AFPs and epoch-finish signals.

| Key | Value | Written by | Notes |
|-----|-------|-----------|-------|
| `AFP:{blockId}` | `AggregatedFinalizationProof` JSON | `share_block_and_grab_proofs.go`, `websocket_pack/routes.go` | AFP for a specific block |
| `EPOCH_FINISH:{epochId}` | `"TRUE"` string | `epoch_rotation.go` | Signal that epoch has rotated (stops AFP/ALFP production) |
| `EPOCH_DATA:{epochId}` | `NextEpochDataHandler` JSON | `epoch_rotation.go` | Next epoch data (quorum, leaders, delayed txs) — used for EpochDataAttestation signing |

**Recovery**: Wiped entirely. AFPs are available from PoD and quorum nodes. Epoch rotation data is rebuilt from fresh genesis.

---

## 4. APPROVEMENT_THREAD_METADATA

Approvement thread state, epoch snapshots, validator storage (AT copy), and helper data.

| Key | Value | Written by | Notes |
|-----|-------|-----------|-------|
| `AT` | `ApprovementThreadMetadataHandler` JSON | `epoch_rotation.go`, `leader_rotation.go`, `entrypoint.go` | Full approvement thread metadata |
| `EPOCH_HANDLER:{epochId}` | `EpochDataSnapshot` JSON | `epoch_rotation.go`, `entrypoint.go` | Epoch snapshot (handler + network params) — used by API, finalization, dashboard |
| `EPOCH_DATA:{epochId}` | `NextEpochDataHandler` JSON | `epoch_rotation.go` | Pre-computed next epoch info — used for EpochDataAttestation signing and verification |
| `VALIDATOR_STORAGE:{pubkey}` | Validator JSON | `epoch_rotation.go` (via batch), `db_interaction.go` | Validator data — AT copy (may differ from ET copy during epoch) |
| `LATEST_BATCH_INDEX` | 8-byte BigEndian int64 | `epoch_rotation.go` | Monotonic batch counter for delayed tx ordering |
| `FIRST_BLOCK_DATA:{epochId}` | `FirstBlockData` JSON | `first_block_in_epoch_monitor.go` | First block hash/timestamp per epoch (used to derive next epoch hash) |

**Recovery**: Wiped entirely. AT restarts from fresh genesis.

---

## 5. FINALIZATION_THREAD_METADATA

All voting/finalization-related data: proofs grabber state, ALFPs, height attestations, last mile sequence, PoD outbox, and ALFP inclusion tracking.

| Key | Value | Written by | Notes |
|-----|-------|-----------|-------|
| `{epochId}:{creator}` | `VotingStat` JSON (`{index, hash, afp}`) | `websocket_pack/routes.go` | Per-leader voting progress within an epoch |
| `{epochId}:PROOFS_GRABBER` | `ProofsGrabber` JSON | `share_block_and_grab_proofs.go` | AFP collection progress tracker per epoch |
| `ALFP:{epochId}:{leader}` | `AggregatedLeaderFinalizationProof` JSON | `leader_finalization.go` | Locally stored ALFP |
| `ALFP_PROGRESS` | Epoch id string | `leader_finalization.go` | Tracks which epoch the ALFP process has reached |
| `ALFP_WATCHER_STATE:{epochId}` | `AlfpWatcherState` JSON | `alfp_inclusion_watcher.go` | Tracks ALFP → anchor block inclusion progress |
| `ALFP_INCLUDED:{epochId}:{leader}` | one-byte marker | `alfp_inclusion.go` | Marks that at least one valid ALFP for this leader was included in an accepted anchor block |
| `HEIGHT_ATTESTATION:{height}` | `HeightAttestation` JSON | `last_mile_finalizer.go`, `block_execution.go` | Quorum-signed height → blockId mapping proof |
| `EPOCH_DATA_ATTESTATION:{epochId}` | `EpochDataAttestation` JSON | `last_mile_finalizer.go`, `block_execution.go` | Quorum-signed next epoch data — ET uses this for cryptographic epoch transition verification |
| `HEIGHT_ATTESTATION_VOTER_STATE` | JSON state | `last_mile_finalizer.go` | Tracks which heights have been voted on |
| `LAST_MILE_FINALIZER_TRACKER` | `LastMileSequenceState` JSON | `last_mile_sequence.go` | Tracks the last mile finalizer's progress (NextHeight, LastBlocksByLeaders) |
| `LAST_MILE_HEIGHT_MAP:{height}` | `blockId` string | `last_mile_sequence.go` | Pre-computed height → blockId mapping (from sequence alignment) |
| `ANCHOR_EPOCH_ACK:{epochId}` | `AnchorEpochAckProof` JSON | `last_mile_finalizer.go`, `websocket_pack/routes.go` | Aggregated anchor signatures confirming epoch transition receipt — gates new quorum voting |
| `POD_OUTBOX:{id}` | Raw WS message bytes | `pod_outbox.go` | Outbox queue for reliable delivery to PoD |

**Recovery**: Wiped entirely. All voting/finalization state is rebuilt from scratch.

---

## Visual Summary

```
┌──────────────────────────────────────────────────────────────────────┐
│                          RECOVERY ACTION                             │
├──────────────────────┬───────────────────────────────────────────────┤
│  BLOCKS              │  WIPE                                         │
│  STATE               │  PRESERVE (reset CHAIN_CURSOR.EpochDataHandler│
│                      │  and bump HeightOffset / EpochOffset)         │
│  EPOCH_DATA          │  WIPE                                         │
│  APPROVEMENT_THREAD_ │  WIPE                                         │
│  METADATA            │                                               │
│  FINALIZATION_       │  WIPE                                         │
│  THREAD_METADATA     │                                               │
└──────────────────────┴───────────────────────────────────────────────┘
```

### Keys to KEEP in STATE during recovery:

- `BLOCK_INDEX:{height}` — historical height mappings (use absolute height)
- `TX:{hash}` — transaction receipts
- `EPOCH_STATS:{epochId}` — historical epoch statistics (use absolute epoch id)
- `EPOCH_DATA:{epochId}` — historical epoch snapshots (use absolute epoch id)
- `VALIDATOR_STORAGE:{pubkey}` — latest validator state
- `{accountPubkey}` — latest account balances
- `CHAIN_CURSOR` — preserve `Statistics`, `NetworkParameters`, `HeightOffset`, `EpochOffset`; reset `EpochDataHandler` (Hash == "") and `EpochStatistics`; bump offsets and overwrite `NetworkId` / `CoreMajorVersion` from the new genesis

### Keys to REMOVE or RESET in STATE during recovery:

- `DELAYED_TRANSACTIONS:{epochId}` — stale delayed tx queues from pre-crash epochs
