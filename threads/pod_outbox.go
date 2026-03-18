// Thread to retry sending pending Proof-of-Delivery messages from the outbox
package threads

import (
	"time"

	"github.com/modulrcloud/modulr-core/utils"
)

// PoDOutboxThread retries pending PoD messages persisted in the outbox.
func PoDOutboxThread() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		_ = utils.FlushPoDOutboxOnce(50)
	}
}
