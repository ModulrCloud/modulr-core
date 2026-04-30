# Recovery Description

This document gives a short, schematic view of the recovery flow after a `modulr-core` network crash.

## Goal

After a crash, operators need to answer two questions:

1. What was the latest valid `modulr-core` epoch known by the network?
2. What was the latest finalized absolute height in that epoch?

Recovery uses `modulr-anchors-core` first, because anchors persist `AggregatedEpochRotationProof` data received from `modulr-core`. That proof tells us the latest known core epoch transition and the validator data needed to query the correct core quorum.

## Phase 1: Discover the Latest Core Epoch

The recovery script queries anchor nodes for their latest known core quorum data.

```mermaid
sequenceDiagram
    participant Script as Recovery script
    participant A1 as Anchor 1
    participant A2 as Anchor 2
    participant A3 as Anchor 3

    Script->>A1: GET /recovery/latest_core_quorum
    Script->>A2: GET /recovery/latest_core_quorum
    Script->>A3: GET /recovery/latest_core_quorum

    A1-->>Script: Signed latest AERP payload
    A2-->>Script: Signed latest AERP payload
    A3-->>Script: Signed latest AERP payload
```

The script verifies anchor signatures and groups responses by the reported `AggregatedEpochRotationProof`.

```mermaid
flowchart TD
    A["Anchor responses"]
    B["Verify anchor signatures"]
    C["Group by AERP<br/>NextEpochId + NextEpochHash"]
    D{"Majority agrees?"}
    E["Latest known core epoch data"]
    F["Recovery cannot continue safely"]

    A --> B --> C --> D
    D -- yes --> E
    D -- no --> F
```

The winning AERP gives the recovery process the latest known core epoch data:

- latest epoch id
- latest epoch hash
- next quorum
- next leaders sequence
- validator HTTP/WSS endpoints collected by anchors

## Phase 2: Query the Latest Core Quorum

Once the script knows the latest core quorum, it queries those validators for their latest finalized height.

```mermaid
flowchart LR
    A["Winning AERP from anchors"]
    B["Extract latest core quorum"]
    C["Collect validator endpoints"]
    D["Query validators:<br/>/recovery/last_finalized_height"]
    E["Verify validator signatures"]
    F["Majority height result"]

    A --> B --> C --> D --> E --> F
```

The script tries all known HTTP endpoints for each validator until one succeeds.

```mermaid
sequenceDiagram
    participant Script as Recovery script
    participant V1 as Core validator 1
    participant V2 as Core validator 2
    participant V3 as Core validator 3

    Script->>V1: GET /recovery/last_finalized_height
    Script->>V2: GET /recovery/last_finalized_height
    Script->>V3: GET /recovery/last_finalized_height

    V1-->>Script: Signed height payload
    V2-->>Script: Signed height payload
    V3-->>Script: Signed height payload
```

The script verifies validator signatures and groups responses by height payload.

```mermaid
flowchart TD
    A["Signed height responses"]
    B["Verify validator signatures"]
    C["Group by:<br/>lastHeight + blockId + blockHash + epochId"]
    D{"Core quorum majority agrees?"}
    E["Recovery point:<br/>epoch Y, height X"]
    F["Recovery point is unsafe"]

    A --> B --> C --> D
    D -- yes --> E
    D -- no --> F
```

## End-to-End Recovery View

```mermaid
flowchart TD
    A["Network crashed"]
    B["Query anchors for latest AERP"]
    C["Verify anchor signatures"]
    D["Select majority AERP"]
    E["Extract latest core quorum<br/>and validator endpoints"]
    F["Query core quorum for<br/>last finalized height"]
    G["Verify validator signatures"]
    H["Select majority height"]
    I["Recovery result:<br/>latest epoch Y, latest height X"]

    A --> B --> C --> D --> E --> F --> G --> H --> I
```

## Result

The recovery script produces the two values needed to restart the network linearly:

- `Y`: the latest valid core epoch known through anchors.
- `X`: the latest finalized absolute height agreed by the latest core quorum.

Operators can then prepare node state for the next era:

1. Preserve `STATE`.
2. Reset ephemeral databases.
3. Set `CHAIN_CURSOR.EpochOffset = Y + 1`.
4. Set `CHAIN_CURSOR.Statistics.LastHeight = X`.
5. Clear `CHAIN_CURSOR.EpochDataHandler` so the new genesis initializes the next era.
6. Start the network with the new `genesis.json`.

After startup, the new era begins at absolute epoch `Y+1` and absolute height `X+1`.
