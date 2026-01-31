package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/modulrcloud/modulr-core/constants"
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
	MAX_RETRIES         = 3
	RETRY_INTERVAL      = 200 * time.Millisecond
	READ_WRITE_DEADLINE = 2 * time.Second // timeout for read/write operations for POD (point of distribution)
)

var (
	POD_ACCESS_MUTEX         sync.Mutex      // Guards open/close & replace of PoD conn
	POD_READ_WRITE_MUTEX     sync.Mutex      // Single request (write+read) guarantee for PoD
	POD_WEBSOCKET_CONNECTION *websocket.Conn // Connection with PoD itself
)

// Dedicated "bulk" PoD connection used for high-frequency requests (e.g. block execution fetching blocks).
// This avoids head-of-line blocking with other PoD requests that share the default connection.
var (
	POD_BULK_ACCESS_MUTEX         sync.Mutex
	POD_BULK_READ_WRITE_MUTEX     sync.Mutex
	POD_WEBSOCKET_CONNECTION_BULK *websocket.Conn
)

var (
	ANCHORS_POD_ACCESS_MUTEX         sync.Mutex      // Guards open/close & replace of Anchors PoD conn
	ANCHORS_POD_READ_WRITE_MUTEX     sync.Mutex      // Single request (write+read) guarantee for Anchors PoD
	ANCHORS_POD_WEBSOCKET_CONNECTION *websocket.Conn // Connection with anchors PoD itself
)

// Shared map for per-validator write mutexes.
// It must be global so that different QuorumWaiters/guards still serialize writes
// for the same validator. Keyed by validator ID (pubkey), not connection pointer.
// Memory growth is bounded by the number of unique validators ever seen (~200 bytes each).
var SHARED_WS_WRITE_MU = &sync.Map{}

type WebsocketGuards struct {
	ConnMu *sync.RWMutex
	// WriteMu keys are validator ID strings (pubkeys) and values are *sync.Mutex.
	// Keying by ID (not connection pointer) avoids race conditions when connections
	// are replaced or closed while goroutines still hold references to old connections.
	WriteMu *sync.Map
}

func NewWebsocketGuards() *WebsocketGuards {
	return &WebsocketGuards{
		ConnMu:  &sync.RWMutex{},
		WriteMu: SHARED_WS_WRITE_MU,
	}
}

func SendWebsocketMessageToPoD(msg []byte) ([]byte, error) {

	for attempt := 1; attempt <= MAX_RETRIES; attempt++ {

		POD_ACCESS_MUTEX.Lock()

		if POD_WEBSOCKET_CONNECTION == nil {

			conn, err := openWebsocketConnectionWithPoD()

			if err != nil {
				LogWithTimeThrottled(
					"POD:WS:DIAL",
					2*time.Second,
					fmt.Sprintf("PoD websocket dial failed (attempt %d/%d): %v", attempt, MAX_RETRIES, err),
					YELLOW_COLOR,
				)

				POD_ACCESS_MUTEX.Unlock()

				time.Sleep(RETRY_INTERVAL)

				continue
			}

			POD_WEBSOCKET_CONNECTION = conn

		}

		c := POD_WEBSOCKET_CONNECTION

		POD_ACCESS_MUTEX.Unlock()

		// single request (write+read) for this connection
		POD_READ_WRITE_MUTEX.Lock()

		_ = c.SetWriteDeadline(time.Now().Add(READ_WRITE_DEADLINE))

		err := c.WriteMessage(websocket.TextMessage, msg)

		if err != nil {
			LogWithTimeThrottled(
				"POD:WS:WRITE",
				2*time.Second,
				fmt.Sprintf("PoD websocket write failed (attempt %d/%d): %v", attempt, MAX_RETRIES, err),
				YELLOW_COLOR,
			)
			POD_READ_WRITE_MUTEX.Unlock()
			POD_ACCESS_MUTEX.Lock()
			if POD_WEBSOCKET_CONNECTION == c {
				_ = c.Close()
				POD_WEBSOCKET_CONNECTION = nil
			}
			POD_ACCESS_MUTEX.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		_ = c.SetReadDeadline(time.Now().Add(READ_WRITE_DEADLINE))
		_, resp, err := c.ReadMessage()

		POD_READ_WRITE_MUTEX.Unlock()

		if err != nil {
			LogWithTimeThrottled(
				"POD:WS:READ",
				2*time.Second,
				fmt.Sprintf("PoD websocket read failed (attempt %d/%d): %v", attempt, MAX_RETRIES, err),
				YELLOW_COLOR,
			)
			POD_ACCESS_MUTEX.Lock()
			if POD_WEBSOCKET_CONNECTION == c {
				_ = c.Close()
				POD_WEBSOCKET_CONNECTION = nil
			}
			POD_ACCESS_MUTEX.Unlock()
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

		POD_BULK_ACCESS_MUTEX.Lock()

		if POD_WEBSOCKET_CONNECTION_BULK == nil {

			conn, err := openWebsocketConnectionWithPoD()

			if err != nil {
				LogWithTimeThrottled(
					"POD:BULK:WS:DIAL",
					2*time.Second,
					fmt.Sprintf("PoD bulk websocket dial failed (attempt %d/%d): %v", attempt, MAX_RETRIES, err),
					YELLOW_COLOR,
				)

				POD_BULK_ACCESS_MUTEX.Unlock()

				time.Sleep(RETRY_INTERVAL)

				continue
			}

			POD_WEBSOCKET_CONNECTION_BULK = conn

		}

		c := POD_WEBSOCKET_CONNECTION_BULK

		POD_BULK_ACCESS_MUTEX.Unlock()

		// single request (write+read) for this connection
		POD_BULK_READ_WRITE_MUTEX.Lock()

		_ = c.SetWriteDeadline(time.Now().Add(READ_WRITE_DEADLINE))

		err := c.WriteMessage(websocket.TextMessage, msg)

		if err != nil {
			LogWithTimeThrottled(
				"POD:BULK:WS:WRITE",
				2*time.Second,
				fmt.Sprintf("PoD bulk websocket write failed (attempt %d/%d): %v", attempt, MAX_RETRIES, err),
				YELLOW_COLOR,
			)
			POD_BULK_READ_WRITE_MUTEX.Unlock()
			POD_BULK_ACCESS_MUTEX.Lock()
			if POD_WEBSOCKET_CONNECTION_BULK == c {
				_ = c.Close()
				POD_WEBSOCKET_CONNECTION_BULK = nil
			}
			POD_BULK_ACCESS_MUTEX.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		_ = c.SetReadDeadline(time.Now().Add(READ_WRITE_DEADLINE))
		_, resp, err := c.ReadMessage()

		POD_BULK_READ_WRITE_MUTEX.Unlock()

		if err != nil {
			LogWithTimeThrottled(
				"POD:BULK:WS:READ",
				2*time.Second,
				fmt.Sprintf("PoD bulk websocket read failed (attempt %d/%d): %v", attempt, MAX_RETRIES, err),
				YELLOW_COLOR,
			)
			POD_BULK_ACCESS_MUTEX.Lock()
			if POD_WEBSOCKET_CONNECTION_BULK == c {
				_ = c.Close()
				POD_WEBSOCKET_CONNECTION_BULK = nil
			}
			POD_BULK_ACCESS_MUTEX.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		return resp, nil
	}

	LogWithTimeThrottled(
		"POD:BULK:WS:FAILED",
		2*time.Second,
		fmt.Sprintf("PoD bulk websocket request failed after %d attempts", MAX_RETRIES),
		RED_COLOR,
	)
	return nil, fmt.Errorf("failed to send message after %d attempts", MAX_RETRIES)

}

func SendWebsocketMessageToAnchorsPoD(msg []byte) ([]byte, error) {

	for attempt := 1; attempt <= MAX_RETRIES; attempt++ {

		ANCHORS_POD_ACCESS_MUTEX.Lock()

		if ANCHORS_POD_WEBSOCKET_CONNECTION == nil {

			conn, err := openWebsocketConnectionWithAnchorsPoD()

			if err != nil {
				LogWithTimeThrottled(
					"ANCHORS_POD:WS:DIAL",
					2*time.Second,
					fmt.Sprintf("Anchors-PoD websocket dial failed (attempt %d/%d): %v", attempt, MAX_RETRIES, err),
					YELLOW_COLOR,
				)

				ANCHORS_POD_ACCESS_MUTEX.Unlock()

				time.Sleep(RETRY_INTERVAL)

				continue
			}

			ANCHORS_POD_WEBSOCKET_CONNECTION = conn

		}

		c := ANCHORS_POD_WEBSOCKET_CONNECTION

		ANCHORS_POD_ACCESS_MUTEX.Unlock()

		// single request (write+read) for this connection
		ANCHORS_POD_READ_WRITE_MUTEX.Lock()

		_ = c.SetWriteDeadline(time.Now().Add(READ_WRITE_DEADLINE))

		err := c.WriteMessage(websocket.TextMessage, msg)

		if err != nil {
			LogWithTimeThrottled(
				"ANCHORS_POD:WS:WRITE",
				2*time.Second,
				fmt.Sprintf("Anchors-PoD websocket write failed (attempt %d/%d): %v", attempt, MAX_RETRIES, err),
				YELLOW_COLOR,
			)
			ANCHORS_POD_READ_WRITE_MUTEX.Unlock()
			ANCHORS_POD_ACCESS_MUTEX.Lock()
			if ANCHORS_POD_WEBSOCKET_CONNECTION == c {
				_ = c.Close()
				ANCHORS_POD_WEBSOCKET_CONNECTION = nil
			}
			ANCHORS_POD_ACCESS_MUTEX.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		_ = c.SetReadDeadline(time.Now().Add(READ_WRITE_DEADLINE))
		_, resp, err := c.ReadMessage()

		ANCHORS_POD_READ_WRITE_MUTEX.Unlock()

		if err != nil {
			LogWithTimeThrottled(
				"ANCHORS_POD:WS:READ",
				2*time.Second,
				fmt.Sprintf("Anchors-PoD websocket read failed (attempt %d/%d): %v", attempt, MAX_RETRIES, err),
				YELLOW_COLOR,
			)
			ANCHORS_POD_ACCESS_MUTEX.Lock()
			if ANCHORS_POD_WEBSOCKET_CONNECTION == c {
				_ = c.Close()
				ANCHORS_POD_WEBSOCKET_CONNECTION = nil
			}
			ANCHORS_POD_ACCESS_MUTEX.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		return resp, nil

	}

	LogWithTimeThrottled(
		"ANCHORS_POD:WS:FAILED",
		2*time.Second,
		fmt.Sprintf("Anchors-PoD websocket request failed after %d attempts", MAX_RETRIES),
		RED_COLOR,
	)
	return nil, fmt.Errorf("failed to send message after %d attempts", MAX_RETRIES)

}

func OpenWebsocketConnectionsWithQuorum(quorum []string, wsConnMap map[string]*websocket.Conn, guards *WebsocketGuards) {
	// Close and remove any existing connections
	guards.ConnMu.Lock()
	for id, conn := range wsConnMap {
		if conn != nil {
			_ = conn.Close()
		}
		delete(wsConnMap, id)
	}
	guards.ConnMu.Unlock()

	// Note: We intentionally do NOT clear per-validator mutexes from SHARED_WS_WRITE_MU.
	// Memory growth is minimal (~200 bytes per validator) and bounded by total validators seen.
	// This avoids any potential race conditions and Wait() overhead.

	// Establish new connections for each validator in the quorum
	for _, validatorPubkey := range quorum {
		// Fetch validator metadata
		raw, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte(constants.DBKeyPrefixValidatorStorage+validatorPubkey), nil)
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

// SendAndWaitValidated is similar to SendAndWait but only counts validated responses toward the majority.
// The validate callback is called asynchronously in a goroutine for each response, allowing early exit
// once majority of validated responses is reached. This prevents attacks where malicious nodes respond
// quickly with invalid data to trigger early exit.
func (qw *QuorumWaiter) SendAndWaitValidated(
	ctx context.Context, message []byte, quorum []string,
	wsConnMap map[string]*websocket.Conn, majority int,
	validate func(id string, raw []byte) bool,
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

	// Separate tracking for validated responses
	validAnswered := make(map[string]struct{})
	validResponses := make(map[string][]byte)
	validMu := sync.Mutex{}

	// Channel for validated responses
	validCh := make(chan struct {
		id  string
		msg []byte
	}, len(quorum))

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
			// Mark as answered (for resend logic)
			qw.mu.Lock()
			if _, ok := qw.answered[r.id]; !ok {
				qw.answered[r.id] = struct{}{}
				qw.responses[r.id] = r.msg
			}
			qw.mu.Unlock()

			// Validate asynchronously in goroutine
			go func(id string, raw []byte) {
				if validate(id, raw) {
					validMu.Lock()
					if _, ok := validAnswered[id]; !ok {
						validAnswered[id] = struct{}{}
						validResponses[id] = raw
						validMu.Unlock()

						// Send validated response to channel
						select {
						case validCh <- struct {
							id  string
							msg []byte
						}{id: id, msg: raw}:
						case <-qw.done:
						}
					} else {
						validMu.Unlock()
					}
				}
			}(r.id, r.msg)

		case <-validCh:
			// Check if we reached majority of validated responses
			validMu.Lock()
			validCount := len(validAnswered)
			validMu.Unlock()

			if validCount >= majority {
				close(qw.done)
				// Copy validated responses
				validMu.Lock()
				out := make(map[string][]byte, len(validResponses))
				for k, v := range validResponses {
					out[k] = v
				}
				validMu.Unlock()

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
				// Check if we have enough validated responses before giving up
				validMu.Lock()
				validCount := len(validAnswered)
				validMu.Unlock()

				if validCount >= majority {
					close(qw.done)
					validMu.Lock()
					out := make(map[string][]byte, len(validResponses))
					for k, v := range validResponses {
						out[k] = v
					}
					validMu.Unlock()
					qw.reconnectFailed(wsConnMap)
					return out, true
				}

				qw.reconnectFailed(wsConnMap)
				return nil, false
			}
			qw.timer.Reset(time.Second)
			qw.sendMessages(qw.buf, message, wsConnMap)

		case <-ctx.Done():
			// Check if we have enough validated responses before timeout
			validMu.Lock()
			validCount := len(validAnswered)
			validMu.Unlock()

			if validCount >= majority {
				close(qw.done)
				validMu.Lock()
				out := make(map[string][]byte, len(validResponses))
				for k, v := range validResponses {
					out[k] = v
				}
				validMu.Unlock()
				qw.reconnectFailed(wsConnMap)
				return out, true
			}

			qw.reconnectFailed(wsConnMap)
			return nil, false
		}
	}
}

// getWriteMuById returns a per-validator mutex.
// Keying by ID (pubkey) instead of connection pointer avoids the race where:
// 1. Goroutine A loads mutex M for connection C
// 2. Goroutine B (on error) deletes M from the map
// 3. Goroutine C creates new mutex M2 for the same connection
// 4. A and C now have different mutexes for the same connection → panic
func (qw *QuorumWaiter) getWriteMuById(id string) *sync.Mutex {
	if m, ok := qw.guards.WriteMu.Load(id); ok {
		return m.(*sync.Mutex)
	}
	m := &sync.Mutex{}
	actual, _ := qw.guards.WriteMu.LoadOrStore(id, m)
	return actual.(*sync.Mutex)
}

func reconnectOnce(pubkey string, wsConnMap map[string]*websocket.Conn, guards *WebsocketGuards) {

	// Get validator metadata
	raw, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte(constants.DBKeyPrefixValidatorStorage+pubkey), nil)
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
	if old := wsConnMap[pubkey]; old != nil {
		_ = old.Close()
	}
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
			// The mutex is keyed by validator ID (not connection pointer) to avoid race conditions
			// when connections are replaced or closed.
			wmu := qw.getWriteMuById(id)
			wmu.Lock()

			// Re-check connection validity under lock - it may have been replaced/closed
			qw.guards.ConnMu.RLock()
			currentConn, ok := wsConnMap[id]
			qw.guards.ConnMu.RUnlock()
			if !ok || currentConn != c {
				// Connection was replaced, abort this request
				wmu.Unlock()
				return
			}

			_ = c.SetWriteDeadline(time.Now().Add(READ_WRITE_DEADLINE))
			err := c.WriteMessage(websocket.TextMessage, msg)
			if err != nil {
				wmu.Unlock()
				// Mark as failed and remove the connection safely
				qw.mu.Lock()
				qw.failed[id] = struct{}{}
				qw.mu.Unlock()

				qw.guards.ConnMu.Lock()
				if wsConnMap[id] == c {
					_ = c.Close()
					delete(wsConnMap, id)
				}
				qw.guards.ConnMu.Unlock()
				return
			}

			// Short read deadline for reply
			_ = c.SetReadDeadline(time.Now().Add(READ_WRITE_DEADLINE))
			_, raw, err := c.ReadMessage()
			wmu.Unlock()
			if err != nil {
				// Mark as failed and remove the connection safely
				qw.mu.Lock()
				qw.failed[id] = struct{}{}
				qw.mu.Unlock()

				qw.guards.ConnMu.Lock()
				if wsConnMap[id] == c {
					_ = c.Close()
					delete(wsConnMap, id)
				}
				qw.guards.ConnMu.Unlock()
				return
			}

			select {
			case qw.responseCh <- QuorumResponse{id: id, msg: raw}:
			case <-qw.done:
			}
		}(id, conn)
	}
}
