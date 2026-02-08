package threads

import (
	"errors"
	"fmt"
	"strings"

	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
)

func GetLastExecutedHeight() int64 {
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	defer handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()
	if handlers.EXECUTION_THREAD_METADATA.Handler.Statistics == nil {
		return -1
	}
	return handlers.EXECUTION_THREAD_METADATA.Handler.Statistics.LastHeight
}

// EnqueueEVMSignedTx wraps an Ethereum signed tx (0x...) into the Modulr mempool as a normal transaction
// whose payload is interpreted by the embedded EVM execution path.
func EnqueueEVMSignedTx(serializedTx0x string) error {
	serializedTx0x = strings.TrimSpace(serializedTx0x)
	if !strings.HasPrefix(serializedTx0x, "0x") {
		return errors.New("missing 0x prefix")
	}
	// We keep it minimal: use dummy modulr tx fields; validation for evmRaw will NOT require modulr signature.
	tx := structures.Transaction{
		V:     1,
		From:  globals.CONFIGURATION.PublicKey, // placeholder modulr sender; real EVM sender is inside raw tx
		To:    globals.CONFIGURATION.PublicKey,
		Amount: 0,
		Fee:    0,
		Sig:    "evmraw", // placeholder; not used
		Nonce:  1,        // placeholder; not used
		Payload: map[string]any{
			"type": "evmRaw",
			"raw":  serializedTx0x,
		},
	}

	globals.MEMPOOL.Mutex.Lock()
	defer globals.MEMPOOL.Mutex.Unlock()
	if len(globals.MEMPOOL.Slice) >= globals.CONFIGURATION.TxsMempoolSize {
		return fmt.Errorf("mempool full")
	}
	globals.MEMPOOL.Slice = append(globals.MEMPOOL.Slice, tx)
	return nil
}

