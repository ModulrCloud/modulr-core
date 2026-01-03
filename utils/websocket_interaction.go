package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/gorilla/websocket"
)

type QuorumWaiter struct {
	responseCh chan QuorumResponse
	done       chan struct{}
	answered   map[string]struct{}
	responses  map[string][]byte
	timer      *time.Timer
	mu         sync.Mutex
	buf        []string
	failed     map[string]struct{}
	guards     *WebsocketGuards
}

type QuorumResponse struct {
	id  string
	msg []byte
}

const (
	MAX_RETRIES             = 3
	RETRY_INTERVAL          = 200 * time.Millisecond
	POD_READ_WRITE_DEADLINE = 2 * time.Second // timeout for read/write operations for POD (point of distribution)
)

var (
	POD_MUTEX                sync.Mutex      // Guards open/close & replace of PoD conn
	POD_REQUEST_MUTEX        sync.Mutex      // Single request (write+read) guarantee for PoD
	POD_WEBSOCKET_CONNECTION *websocket.Conn // Connection with PoD itself
)

// Dedicated "bulk" PoD connection used for high-frequency requests (e.g. block execution fetching blocks).
// This avoids head-of-line blocking with other PoD requests that share the default connection.
var (
	POD_BULK_MUTEX                sync.Mutex
	POD_BULK_REQUEST_MUTEX        sync.Mutex
	POD_WEBSOCKET_CONNECTION_BULK *websocket.Conn
)

var (
	ANCHORS_POD_MUTEX                sync.Mutex      // Guards open/close & replace of Anchors PoD conn
	ANCHORS_POD_REQUEST_MUTEX        sync.Mutex      // Single request (write+read) guarantee for Anchors PoD
	ANCHORS_POD_WEBSOCKET_CONNECTION *websocket.Conn // Connection with anchors PoD itself
)

var (
	WEBSOCKET_CONNECTION_MUTEX sync.RWMutex // Protects concurrent access to wsConnMap (map[string]*websocket.Conn)
	// key: pubkey -> *sync.Mutex
	WEBSOCKET_WRITE_MUTEX sync.Map // Ensures single writer per websocket connection (gorilla/websocket requirement)
)

type WebsocketGuards struct {
	ConnMu  *sync.RWMutex
	WriteMu *sync.Map
}

func NewWebsocketGuards() *WebsocketGuards {
	return &WebsocketGuards{
		ConnMu:  &sync.RWMutex{},
		WriteMu: &sync.Map{},
	}
}

func defaultWebsocketGuards() *WebsocketGuards {
	return &WebsocketGuards{
		ConnMu:  &WEBSOCKET_CONNECTION_MUTEX,
		WriteMu: &WEBSOCKET_WRITE_MUTEX,
	}
}

func SendWebsocketMessageToPoD(msg []byte) ([]byte, error) {

	for attempt := 1; attempt <= MAX_RETRIES; attempt++ {

		POD_MUTEX.Lock()

		if POD_WEBSOCKET_CONNECTION == nil {

			conn, err := openWebsocketConnectionWithPoD()

			if err != nil {

				POD_MUTEX.Unlock()

				time.Sleep(RETRY_INTERVAL)

				continue
			}

			POD_WEBSOCKET_CONNECTION = conn

		}

		c := POD_WEBSOCKET_CONNECTION

		POD_MUTEX.Unlock()

		// single request (write+read) for this connection
		POD_REQUEST_MUTEX.Lock()

		_ = c.SetWriteDeadline(time.Now().Add(POD_READ_WRITE_DEADLINE))

		err := c.WriteMessage(websocket.TextMessage, msg)

		if err != nil {
			POD_REQUEST_MUTEX.Unlock()
			POD_MUTEX.Lock()
			if POD_WEBSOCKET_CONNECTION == c {
				_ = c.Close()
				POD_WEBSOCKET_CONNECTION = nil
			}
			POD_MUTEX.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		_ = c.SetReadDeadline(time.Now().Add(POD_READ_WRITE_DEADLINE))
		_, resp, err := c.ReadMessage()

		POD_REQUEST_MUTEX.Unlock()

		if err != nil {
			POD_MUTEX.Lock()
			if POD_WEBSOCKET_CONNECTION == c {
				_ = c.Close()
				POD_WEBSOCKET_CONNECTION = nil
			}
			POD_MUTEX.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("failed to send message after %d attempts", MAX_RETRIES)

}

// SendWebsocketMessageToPoDForBlocks is the same as SendWebsocketMessageToPoD but uses a dedicated PoD connection.
// Use it for high-frequency block fetching so it doesn't block other PoD calls (proofs, stores, etc).
func SendWebsocketMessageToPoDForBlocks(msg []byte) ([]byte, error) {

	for attempt := 1; attempt <= MAX_RETRIES; attempt++ {

		POD_BULK_MUTEX.Lock()

		if POD_WEBSOCKET_CONNECTION_BULK == nil {

			conn, err := openWebsocketConnectionWithPoD()

			if err != nil {

				POD_BULK_MUTEX.Unlock()

				time.Sleep(RETRY_INTERVAL)

				continue
			}

			POD_WEBSOCKET_CONNECTION_BULK = conn

		}

		c := POD_WEBSOCKET_CONNECTION_BULK

		POD_BULK_MUTEX.Unlock()

		// single request (write+read) for this connection
		POD_BULK_REQUEST_MUTEX.Lock()

		_ = c.SetWriteDeadline(time.Now().Add(POD_READ_WRITE_DEADLINE))

		err := c.WriteMessage(websocket.TextMessage, msg)

		if err != nil {
			POD_BULK_REQUEST_MUTEX.Unlock()
			POD_BULK_MUTEX.Lock()
			if POD_WEBSOCKET_CONNECTION_BULK == c {
				_ = c.Close()
				POD_WEBSOCKET_CONNECTION_BULK = nil
			}
			POD_BULK_MUTEX.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		_ = c.SetReadDeadline(time.Now().Add(POD_READ_WRITE_DEADLINE))
		_, resp, err := c.ReadMessage()

		POD_BULK_REQUEST_MUTEX.Unlock()

		if err != nil {
			POD_BULK_MUTEX.Lock()
			if POD_WEBSOCKET_CONNECTION_BULK == c {
				_ = c.Close()
				POD_WEBSOCKET_CONNECTION_BULK = nil
			}
			POD_BULK_MUTEX.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("failed to send message after %d attempts", MAX_RETRIES)

}

func SendWebsocketMessageToAnchorsPoD(msg []byte) ([]byte, error) {

	for attempt := 1; attempt <= MAX_RETRIES; attempt++ {

		ANCHORS_POD_MUTEX.Lock()

		if ANCHORS_POD_WEBSOCKET_CONNECTION == nil {

			conn, err := openWebsocketConnectionWithAnchorsPoD()

			if err != nil {

				ANCHORS_POD_MUTEX.Unlock()

				time.Sleep(RETRY_INTERVAL)

				continue
			}

			ANCHORS_POD_WEBSOCKET_CONNECTION = conn

		}

		c := ANCHORS_POD_WEBSOCKET_CONNECTION

		ANCHORS_POD_MUTEX.Unlock()

		// single request (write+read) for this connection
		ANCHORS_POD_REQUEST_MUTEX.Lock()

		_ = c.SetWriteDeadline(time.Now().Add(POD_READ_WRITE_DEADLINE))

		err := c.WriteMessage(websocket.TextMessage, msg)

		if err != nil {
			ANCHORS_POD_REQUEST_MUTEX.Unlock()
			ANCHORS_POD_MUTEX.Lock()
			if ANCHORS_POD_WEBSOCKET_CONNECTION == c {
				_ = c.Close()
				ANCHORS_POD_WEBSOCKET_CONNECTION = nil
			}
			ANCHORS_POD_MUTEX.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		_ = c.SetReadDeadline(time.Now().Add(POD_READ_WRITE_DEADLINE))
		_, resp, err := c.ReadMessage()

		ANCHORS_POD_REQUEST_MUTEX.Unlock()

		if err != nil {
			ANCHORS_POD_MUTEX.Lock()
			if ANCHORS_POD_WEBSOCKET_CONNECTION == c {
				_ = c.Close()
				ANCHORS_POD_WEBSOCKET_CONNECTION = nil
			}
			ANCHORS_POD_MUTEX.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		return resp, nil

	}

	return nil, fmt.Errorf("failed to send message after %d attempts", MAX_RETRIES)

}

func OpenWebsocketConnectionsWithQuorum(quorum []string, wsConnMap map[string]*websocket.Conn, guards *WebsocketGuards) {
	if guards == nil {
		guards = defaultWebsocketGuards()
	}
	// Close and remove any existing connections (called once per your note)
	guards.ConnMu.Lock()
	for id, conn := range wsConnMap {
		if conn != nil {
			_ = conn.Close()
		}
		delete(wsConnMap, id)
		// Also drop per-connection write mutex to prevent unbounded growth when pubkeys change.
		guards.WriteMu.Delete(id)
	}
	guards.ConnMu.Unlock()

	// Establish new connections for each validator in the quorum
	for _, validatorPubkey := range quorum {
		// Fetch validator metadata
		raw, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte(validatorPubkey+"_VALIDATOR_STORAGE"), nil)
		if err != nil {
			continue
		}

		// Parse metadata
		var validatorStorage structures.ValidatorStorage
		if err := json.Unmarshal(raw, &validatorStorage); err != nil {
			continue
		}

		// Skip if no WS URL
		if validatorStorage.WssValidatorUrl == "" {
			continue
		}

		// Dial
		conn, _, err := websocket.DefaultDialer.Dial(validatorStorage.WssValidatorUrl, nil)
		if err != nil {
			continue
		}

		// Store in the shared map under lock
		guards.ConnMu.Lock()
		wsConnMap[validatorPubkey] = conn
		guards.ConnMu.Unlock()
	}
}

func NewQuorumWaiter(maxQuorumSize int, guards *WebsocketGuards) *QuorumWaiter {
	if guards == nil {
		guards = defaultWebsocketGuards()
	}
	return &QuorumWaiter{
		responseCh: make(chan QuorumResponse, maxQuorumSize),
		done:       make(chan struct{}),
		answered:   make(map[string]struct{}, maxQuorumSize),
		responses:  make(map[string][]byte, maxQuorumSize),
		timer:      time.NewTimer(0),
		buf:        make([]string, 0, maxQuorumSize),
		failed:     make(map[string]struct{}),
		guards:     guards,
	}
}

func (qw *QuorumWaiter) SendAndWait(
	ctx context.Context, message []byte, quorum []string,
	wsConnMap map[string]*websocket.Conn, majority int,
) (map[string][]byte, bool) {

	// Reset state
	qw.mu.Lock()
	for k := range qw.answered {
		delete(qw.answered, k)
	}
	for k := range qw.responses {
		delete(qw.responses, k)
	}
	for k := range qw.failed {
		delete(qw.failed, k)
	}
	qw.buf = qw.buf[:0]
	qw.mu.Unlock()

	// Arm/Reset timer
	if !qw.timer.Stop() {
		select {
		case <-qw.timer.C:
		default:
		}
	}
	qw.timer.Reset(time.Second)
	qw.done = make(chan struct{})

	// First send to the whole quorum
	qw.sendMessages(quorum, message, wsConnMap)

	for {
		select {
		case r := <-qw.responseCh:
			qw.mu.Lock()
			if _, ok := qw.answered[r.id]; !ok {
				qw.answered[r.id] = struct{}{}
				qw.responses[r.id] = r.msg
			}
			count := len(qw.answered)
			qw.mu.Unlock()

			if count >= majority {
				close(qw.done)
				// copy responses
				qw.mu.Lock()
				out := make(map[string][]byte, len(qw.responses))
				for k, v := range qw.responses {
					out[k] = v
				}
				qw.mu.Unlock()

				// one-shot reconnect of failed nodes
				qw.reconnectFailed(wsConnMap)
				return out, true
			}

		case <-qw.timer.C:
			// resend to unanswered
			qw.mu.Lock()
			qw.buf = qw.buf[:0]
			for _, id := range quorum {
				if _, ok := qw.answered[id]; !ok {
					qw.buf = append(qw.buf, id)
				}
			}
			qw.mu.Unlock()

			if len(qw.buf) == 0 {
				qw.reconnectFailed(wsConnMap)
				return nil, false
			}
			qw.timer.Reset(time.Second)
			qw.sendMessages(qw.buf, message, wsConnMap)

		case <-ctx.Done():
			qw.reconnectFailed(wsConnMap)
			return nil, false
		}
	}
}

func (qw *QuorumWaiter) getWriteMu(id string) *sync.Mutex {
	if m, ok := qw.guards.WriteMu.Load(id); ok {
		return m.(*sync.Mutex)
	}
	m := &sync.Mutex{}
	actual, _ := qw.guards.WriteMu.LoadOrStore(id, m)
	return actual.(*sync.Mutex)
}

func reconnectOnce(pubkey string, wsConnMap map[string]*websocket.Conn, guards *WebsocketGuards) {

	// Get validator metadata
	raw, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte(pubkey+"_VALIDATOR_STORAGE"), nil)
	if err != nil {
		return
	}
	var validatorStorage structures.ValidatorStorage
	if err := json.Unmarshal(raw, &validatorStorage); err != nil || validatorStorage.WssValidatorUrl == "" {
		return
	}

	// Try a single dial attempt
	conn, _, err := websocket.DefaultDialer.Dial(validatorStorage.WssValidatorUrl, nil)
	if err != nil {
		return
	}

	// Store back into the shared map under lock
	guards.ConnMu.Lock()

	wsConnMap[pubkey] = conn

	guards.ConnMu.Unlock()

}

func (qw *QuorumWaiter) reconnectFailed(wsConnMap map[string]*websocket.Conn) {
	qw.mu.Lock()
	failedCopy := make([]string, 0, len(qw.failed))
	for id := range qw.failed {
		failedCopy = append(failedCopy, id)
	}
	// reset failed set for the next round
	for k := range qw.failed {
		delete(qw.failed, k)
	}
	qw.mu.Unlock()

	for _, id := range failedCopy {
		reconnectOnce(id, wsConnMap, qw.guards)
	}
}

func openWebsocketConnectionWithPoD() (*websocket.Conn, error) {
	u, err := url.Parse(globals.CONFIGURATION.PointOfDistributionWS)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("dial error: %w", err)
	}

	return conn, nil
}

func openWebsocketConnectionWithAnchorsPoD() (*websocket.Conn, error) {
	u, err := url.Parse(globals.CONFIGURATION.AnchorsPointOfDistributionWS)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("dial error: %w", err)
	}

	return conn, nil
}

func (qw *QuorumWaiter) sendMessages(targets []string, msg []byte, wsConnMap map[string]*websocket.Conn) {
	for _, id := range targets {
		// Read connection from the shared map under RLock
		qw.guards.ConnMu.RLock()
		conn, ok := wsConnMap[id]
		qw.guards.ConnMu.RUnlock()
		if !ok || conn == nil {
			// Mark as failed so we try to reconnect after the round
			qw.mu.Lock()
			qw.failed[id] = struct{}{}
			qw.mu.Unlock()
			continue
		}

		go func(id string, c *websocket.Conn) {
			// Single-request guard for this websocket.
			// IMPORTANT: gorilla/websocket requires a single reader AND a single writer per connection.
			// We therefore guard the whole request (WriteMessage+ReadMessage), not only the write.
			wmu := qw.getWriteMu(id)
			wmu.Lock()
			_ = c.SetWriteDeadline(time.Now().Add(time.Second))
			err := c.WriteMessage(websocket.TextMessage, msg)
			if err != nil {
				wmu.Unlock()
				// Mark as failed and remove the connection safely
				qw.mu.Lock()
				qw.failed[id] = struct{}{}
				qw.mu.Unlock()

				qw.guards.ConnMu.Lock()
				_ = c.Close()
				delete(wsConnMap, id)
				qw.guards.ConnMu.Unlock()
				qw.guards.WriteMu.Delete(id)
				return
			}

			// Short read deadline for reply
			_ = c.SetReadDeadline(time.Now().Add(time.Second))
			_, raw, err := c.ReadMessage()
			wmu.Unlock()
			if err != nil {
				// Mark as failed and remove the connection safely
				qw.mu.Lock()
				qw.failed[id] = struct{}{}
				qw.mu.Unlock()

				qw.guards.ConnMu.Lock()
				_ = c.Close()
				delete(wsConnMap, id)
				qw.guards.ConnMu.Unlock()
				qw.guards.WriteMu.Delete(id)
				return
			}

			select {
			case qw.responseCh <- QuorumResponse{id: id, msg: raw}:
			case <-qw.done:
			}
		}(id, conn)
	}
}
