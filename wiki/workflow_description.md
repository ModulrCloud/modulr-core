# Modulr Core and Anchors Workflow

This document gives a short, schematic view of the normal interaction between `modulr-core` and `modulr-anchors-core`.

## Why Two Networks

`modulr-core` is the execution and validator network. It produces blocks, finalizes heights, rotates leaders, and changes the validator quorum across epochs.

`modulr-anchors-core` is the anchoring network. It stores compact finality artifacts from `modulr-core` in its own blocks, so that epoch boundaries and leader finalization data are durable outside the core validator set.

The two networks do not need to have the same epoch number or leader schedule. Anchors can run independently and slightly ahead. Their job is not to execute core blocks, but to persist and acknowledge core finality data.

## Validators, Quorum, and Leaders

`modulr-core` has a global validator set. For each epoch, the protocol selects two working groups from that validator set:

1. The **epoch quorum**: validators that vote, sign finality data, and approve epoch rotation.
2. The **leaders sequence**: validators that produce blocks one by one during the epoch.

```mermaid
flowchart TD
    V["Global modulr-core validator set"]

    V --> Q["Epoch N quorum<br/>validators that vote and sign proofs"]
    V --> L["Epoch N leaders sequence<br/>validators that produce blocks"]

    Q --> Q1["Sign AFP / ALFP"]
    Q --> Q2["Sign epoch rotation"]
    L --> L1["Leader 0 window"]
    L --> L2["Leader 1 window"]
    L --> L3["Leader K window"]
```

The quorum and leaders sequence are epoch-scoped. The next epoch can use a different subset and a different leader order.

```mermaid
flowchart LR
    V["Global validator set: A, B, C, D, E, F"]

    subgraph EpochN["Epoch N"]
        QN["Quorum: A, B, C, D"]
        LN["Leaders: A -> C -> B"]
    end

    subgraph EpochNext["Epoch N+1"]
        QNP["Quorum: B, D, E, F"]
        LNP["Leaders: F -> D -> B"]
    end

    V --> QN
    V --> LN
    V --> QNP
    V --> LNP
```

## Normal Epoch Lifecycle

Within one `modulr-core` epoch, leaders rotate one by one. Each leader produces blocks during its leadership window. The core quorum can aggregate finality for individual blocks/heights during that window (AFP). The `AggregatedLeaderFinalizationProof` (ALFP) is built only after the leader's timeframe has ended, because it represents the final block for that leader.

```mermaid
flowchart LR
    subgraph CoreEpoch["modulr-core epoch N"]
        subgraph W1["Leader 0 timeframe"]
            L1["Leader 0<br/>produces blocks"]
            F1["AFP / height finality<br/>during the window"]
        end
        A1["After timeframe ends:<br/>ALFP for Leader 0"]

        subgraph W2["Leader 1 timeframe"]
            L2["Leader 1<br/>produces blocks"]
            F2["AFP / height finality<br/>during the window"]
        end
        A2["After timeframe ends:<br/>ALFP for Leader 1"]

        subgraph W3["Leader K timeframe"]
            L3["Leader K<br/>produces blocks"]
            F3["AFP / height finality<br/>during the window"]
        end
        A3["After timeframe ends:<br/>ALFP for Leader K"]
    end

    L1 --> F1 --> A1
    A1 --> L2 --> F2 --> A2
    A2 --> L3 --> F3 --> A3
```

Each AFP says: "this block/height is finalized". Each ALFP says: "for this leader in this core epoch, this is the last finalized block known by the quorum".

## Anchoring Leader Finalization

ALFPs are sent to `modulr-anchors-core` and included in anchor blocks. This gives the core network an external, durable record of leader finalization.

```mermaid
sequenceDiagram
    participant CoreLeader as Core leader
    participant CoreQuorum as Core quorum
    participant Anchors as modulr-anchors-core

    CoreLeader->>CoreQuorum: Produce and share blocks during leader window
    CoreQuorum->>CoreQuorum: Build AFPs / height proofs during leader window
    Note over CoreLeader,CoreQuorum: Leader timeframe ends
    CoreQuorum->>CoreQuorum: Build ALFP for finished leader
    CoreQuorum->>Anchors: Send ALFP
    Anchors->>Anchors: Include ALFP in anchor block
```

## Recent Changes: Proactive ALFP Requests from `modulr-anchors-core` to `modulr-core`

Historically, ALFP delivery was passive from the anchor perspective: `modulr-core` built an ALFP and pushed it to anchors. Anchors only waited for that HTTP delivery.

Recent changes added a fallback path where anchors can proactively collect missing ALFPs from the core quorum. This is not the primary path; anchors first give the normal `modulr-core` LeaderFinalizationThread a grace period after the leader timeframe ends.

If the ALFP is still missing after that grace period, an anchor:

1. Checks whether the ALFP is already in its mempool or already included in an anchor block.
2. Tries to fetch the ALFP from PoD.
3. If PoD does not have it, opens WebSocket requests to the relevant `modulr-core` quorum.
4. Collects `2/3 + 1` valid leader-finalization signatures.
5. Builds and verifies the ALFP locally.
6. Deposits the ALFP into the anchor mempool so it can be included in an anchor block.

```mermaid
sequenceDiagram
    participant CoreLFT as modulr-core LFT
    participant Anchor as modulr-anchors-core
    participant PoD as PoD
    participant CoreQuorum as Core quorum

    Note over CoreLFT,Anchor: Leader timeframe ends
    CoreLFT->>Anchor: Normal path: POST ALFP
    Anchor->>Anchor: Wait grace period if ALFP is missing

    alt ALFP received normally
        Anchor->>Anchor: Add ALFP to mempool
    else ALFP still missing
        Anchor->>PoD: Try to fetch ALFP
        alt PoD has ALFP
            PoD-->>Anchor: Return ALFP
            Anchor->>Anchor: Verify and add to mempool
        else PoD does not have ALFP
            Anchor->>CoreQuorum: Request leader finalization signatures
            CoreQuorum-->>Anchor: Return signed finalization votes
            Anchor->>Anchor: Build ALFP after 2/3+1 signatures
            Anchor->>Anchor: Verify and add ALFP to mempool
        end
    end
```

```mermaid
flowchart TD
    A["Leader timeframe ends"]
    B["Anchor waits grace period"]
    C{"ALFP already received<br/>or included?"}
    D["Do nothing"]
    E["Try PoD"]
    F{"PoD has ALFP?"}
    G["Verify ALFP<br/>add to anchor mempool"]
    H["Fan out WS requests<br/>to core quorum"]
    I["Collect 2/3+1 signatures"]
    J["Build and verify ALFP"]
    K["Add ALFP to anchor mempool"]
    L["Anchor block includes ALFP"]

    A --> B --> C
    C -- yes --> D
    C -- no --> E --> F
    F -- yes --> G --> L
    F -- no --> H --> I --> J --> K --> L
```

The core finalizer can then observe that all required leaders for the epoch have their ALFPs included by anchors.

```mermaid
flowchart TD
    A["ALFP for leader 0 included"]
    B["ALFP for leader 1 included"]
    C["ALFP for leader K included"]
    D["Core epoch N is ready<br/>for epoch rotation"]

    A --> D
    B --> D
    C --> D
```

## Epoch Rotation Proof

After the core network knows the finalized boundary of the epoch, it builds an `AggregatedEpochRotationProof` (AERP). The AERP describes the transition from epoch `N` to epoch `N+1`, including the next quorum and next leader schedule.

```mermaid
flowchart LR
    A["All leader ALFPs<br/>are available and anchored"]
    B["Core quorum determines<br/>epoch boundary"]
    C["Core quorum signs<br/>epoch rotation"]
    D["AggregatedEpochRotationProof<br/>(N -> N+1)"]

    A --> B --> C --> D
```

The AERP is then sent to `modulr-anchors-core`. Anchors persist it and sign acknowledgements. The core network aggregates those anchor acknowledgements into an `AggregatedAnchorEpochAckProof`.

```mermaid
sequenceDiagram
    participant CoreQuorum as Core quorum
    participant Anchors as modulr-anchors-core
    participant NextCore as Next core epoch

    CoreQuorum->>Anchors: Send AERP (epoch N -> N+1)
    Anchors->>Anchors: Persist AERP
    Anchors-->>CoreQuorum: Sign anchor epoch ACK
    CoreQuorum->>CoreQuorum: Aggregate anchor ACKs
    CoreQuorum->>NextCore: Deliver AggregatedAnchorEpochAckProof
    NextCore->>NextCore: Start sequencing epoch N+1
```

## End-to-End View

```mermaid
flowchart TD
    subgraph CoreN["modulr-core epoch N"]
        B1["Leaders produce blocks"]
        B2["Core quorum finalizes heights"]
        B3["ALFPs are built per leader"]
    end

    subgraph Anchors["modulr-anchors-core"]
        C1["Anchor blocks include ALFPs"]
        C2["Anchors persist AERP"]
        C3["Anchors sign epoch ACKs"]
    end

    subgraph CoreNext["modulr-core epoch N+1"]
        D1["Core receives AggregatedAnchorEpochAckProof"]
        D2["Next epoch starts sequencing"]
    end

    B1 --> B2 --> B3
    B3 --> C1
    C1 --> B4["Core builds AggregatedEpochRotationProof"]
    B4 --> C2 --> C3
    C3 --> D1 --> D2
```

## Key Invariant

`modulr-core` should not treat the next epoch as fully active for sequencing until the epoch rotation has been acknowledged by a majority of anchors.

In short:

1. Core leaders produce blocks.
2. Core quorum finalizes heights.
3. Core quorum creates ALFPs for finished leaders.
4. Anchors include ALFPs in anchor blocks.
5. Core quorum creates the AERP for the next epoch.
6. Anchors persist the AERP and sign ACKs.
7. Core aggregates anchor ACKs.
8. The next core epoch starts sequencing.
