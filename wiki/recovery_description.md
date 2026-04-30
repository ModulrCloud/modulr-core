# Recovery Description

This document gives a short, schematic view of the recovery flow after a `modulr-core` network crash.

## Goal

After a crash, operators need to answer two questions:

1. What was the latest valid `modulr-core` epoch known by the network?
2. What was the latest finalized absolute height in that epoch?

Recovery uses `modulr-anchors-core` first, because anchors persist `AggregatedEpochRotationProof` data received from `modulr-core`. That proof tells us the latest known core epoch transition and the validator data needed to query the correct core quorum.

## Phase 1: Discover the Latest Core Epoch

The recovery script queries anchor nodes for their latest known core quorum data.

These queries are possible because every normal `modulr-core` epoch rotation is anchored first:

1. The core quorum builds an `AggregatedEpochRotationProof` (AERP) for epoch `N -> N+1`.
2. The core quorum sends that AERP to `modulr-anchors-core`.
3. Anchors persist the AERP and sign acknowledgements.
4. `modulr-core` waits for a majority of anchor acknowledgements.
5. Only after that majority ACK exists can the core network start sequencing blocks for epoch `N+1`.

This means the latest core epoch accepted by the running network must already be known by a majority of anchors.

```mermaid
sequenceDiagram
    participant Core as modulr-core quorum
    participant Anchors as modulr-anchors-core majority
    participant NextEpoch as Core epoch N+1

    Core->>Core: Build AERP (N -> N+1)
    Core->>Anchors: Send AERP
    Anchors->>Anchors: Persist AERP
    Anchors-->>Core: Return majority anchor ACKs
    Core->>Core: Build AggregatedAnchorEpochAckProof
    Core->>NextEpoch: Start sequencing epoch N+1
```

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

Under the normal fault model, recovery should eventually be able to discover this data. Since `modulr-core` requires a majority of anchor ACKs before moving to the next epoch, and less than one third of anchors are assumed malicious, that majority contains at least one honest anchor. An honest anchor can provide a valid AERP chain, allowing lagging anchors to align their local recovery state and allowing the script to obtain a majority agreement on the latest epoch.

```mermaid
flowchart TD
    A["Core moved to epoch N+1"]
    B["Therefore: majority anchors<br/>ACKed AERP N -> N+1"]
    C["Fault model:<br/>< 1/3 malicious anchors"]
    D["At least one ACKing anchor<br/>is honest"]
    E["Honest anchor has valid AERP chain"]
    F["Lagging anchors can align"]
    G["Recovery script obtains<br/>majority agreement"]

    A --> B --> D
    C --> D
    D --> E --> F --> G
```

Example with 5 anchors:

```mermaid
flowchart TD
    subgraph Anchors["Anchor network: 5 nodes"]
        A1["Anchor 1<br/>honest<br/>valid AERP chain"]
        A2["Anchor 2<br/>honest<br/>valid AERP chain"]
        A3["Anchor 3<br/>honest<br/>valid AERP chain"]
        A4["Anchor 4<br/>honest<br/>valid AERP chain"]
        A5["Anchor 5<br/>malicious or stale"]
    end

    R["Recovery script queries<br/>majority = 4 anchors"]
    M["Any 4-of-5 response set<br/>contains at least 3 honest anchors"]
    V["Honest anchors provide<br/>valid AERP chain"]
    E["Script reaches the latest<br/>valid core epoch index"]

    R --> A1
    R --> A2
    R --> A3
    R --> A5

    A1 --> M
    A2 --> M
    A3 --> M
    A5 --> M
    M --> V --> E
```

Because the core network could only enter epoch `N+1` after a majority of anchors acknowledged the AERP for `N -> N+1`, at least one honest anchor in the queried majority can provide the valid proof path. With 5 anchors and fewer than one third malicious, a majority query gives enough honest responses to recover the valid AERP chain up to the latest accepted epoch.

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
