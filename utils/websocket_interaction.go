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

// PodClient manages a single persistent websocket connection to a PoD endpoint
// with automatic reconnection, retries, and serialized request/response access.
type PodClient struct {
	name     string
	dialFunc func() (*websocket.Conn, error)
	accessMu sync.Mutex
	rwMu     sync.Mutex
	conn     *websocket.Conn
}

func NewPodClient(name string, dialFunc func() (*websocket.Conn, error)) *PodClient {
	return &PodClient{name: name, dialFunc: dialFunc}
}

func (pc *PodClient) Send(msg []byte) ([]byte, error) {
	for attempt := 1; attempt <= MAX_RETRIES; attempt++ {
		pc.accessMu.Lock()
		if pc.conn == nil {
			conn, err := pc.dialFunc()
			if err != nil {
				LogWithTimeThrottled(
					pc.name+":WS:DIAL",
					2*time.Second,
					fmt.Sprintf("%s websocket dial failed (attempt %d/%d): %v", pc.name, attempt, MAX_RETRIES, err),
					YELLOW_COLOR,
				)
				pc.accessMu.Unlock()
				time.Sleep(RETRY_INTERVAL)
				continue
			}
			pc.conn = conn
		}
		c := pc.conn
		pc.accessMu.Unlock()

		pc.rwMu.Lock()
		_ = c.SetWriteDeadline(time.Now().Add(READ_WRITE_DEADLINE))
		err := c.WriteMessage(websocket.TextMessage, msg)
		if err != nil {
			LogWithTimeThrottled(
				pc.name+":WS:WRITE",
				2*time.Second,
				fmt.Sprintf("%s websocket write failed (attempt %d/%d): %v", pc.name, attempt, MAX_RETRIES, err),
				YELLOW_COLOR,
			)
			pc.rwMu.Unlock()
			pc.accessMu.Lock()
			if pc.conn == c {
				_ = c.Close()
				pc.conn = nil
			}
			pc.accessMu.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		_ = c.SetReadDeadline(time.Now().Add(READ_WRITE_DEADLINE))
		_, resp, err := c.ReadMessage()
		pc.rwMu.Unlock()

		if err != nil {
			LogWithTimeThrottled(
				pc.name+":WS:READ",
				2*time.Second,
				fmt.Sprintf("%s websocket read failed (attempt %d/%d): %v", pc.name, attempt, MAX_RETRIES, err),
				YELLOW_COLOR,
			)
			pc.accessMu.Lock()
			if pc.conn == c {
				_ = c.Close()
				pc.conn = nil
			}
			pc.accessMu.Unlock()
			time.Sleep(RETRY_INTERVAL)
			continue
		}

		return resp, nil
	}

	LogWithTimeThrottled(
		pc.name+":WS:FAILED",
		2*time.Second,
		fmt.Sprintf("%s websocket request failed after %d attempts", pc.name, MAX_RETRIES),
		RED_COLOR,
	)
	return nil, fmt.Errorf("failed to send %s message after %d attempts", pc.name, MAX_RETRIES)
}

var (
	POD_CLIENT         = NewPodClient("PoD", openWebsocketConnectionWithPoD)
	POD_BULK_CLIENT    = NewPodClient("PoD-Bulk", openWebsocketConnectionWithPoD)
	ANCHORS_POD_CLIENT = NewPodClient("Anchors-PoD", openWebsocketConnectionWithAnchorsPoD)
)

type WebsocketGuards struct {
	ConnMu *sync.RWMutex
	// WriteMu keys are *websocket.Conn pointers, values are *sync.Mutex.
	// Each connection has its own mutex, so replacing a connection doesn't block new operations.
	// IMPORTANT: We never delete synchronously to avoid the race condition where:
	//   1. Goroutine A loads mutex M for connection C
	//   2. Goroutine B (on error) deletes M from the map
	//   3. Goroutine C creates new mutex M2 for the same connection
	//   4. A and C now have different mutexes → concurrent write → panic
	// Cleanup is done asynchronously after a safe delay (3x READ_WRITE_DEADLINE).
	WriteMu *sync.Map
}

func NewWebsocketGuards() *WebsocketGuards {
	return &WebsocketGuards{
		ConnMu:  &sync.RWMutex{},
		WriteMu: &sync.Map{},
	}
}

func SendWebsocketMessageToPoD(msg []byte) ([]byte, error) {
	return POD_CLIENT.Send(msg)
}

// SendWebsocketMessageToPoDForBlocks uses a dedicated PoD connection for high-frequency block fetching
// so it doesn't block other PoD calls (proofs, stores, etc).
func SendWebsocketMessageToPoDForBlocks(msg []byte) ([]byte, error) {
	return POD_BULK_CLIENT.Send(msg)
}

func SendWebsocketMessageToAnchorsPoD(msg []byte) ([]byte, error) {
	return ANCHORS_POD_CLIENT.Send(msg)
}

func OpenWebsocketConnectionsWithQuorum(quorum []string, wsConnMap map[string]*websocket.Conn, guards *WebsocketGuards) {
	// Collect old connections for delayed mutex cleanup
	var oldConns []*websocket.Conn

	// Close and remove any existing connections
	guards.ConnMu.Lock()
	for id, conn := range wsConnMap {
		if conn != nil {
			oldConns = append(oldConns, conn)
			_ = conn.Close()
		}
		delete(wsConnMap, id)
	}
	guards.ConnMu.Unlock()

	// Schedule async cleanup of old connection mutexes.
	// We wait 3x READ_WRITE_DEADLINE to ensure all goroutines have finished.
	// This runs in background and doesn't block.
	if len(oldConns) > 0 {
		go func(conns []*websocket.Conn, writeMu *sync.Map) {
			time.Sleep(3 * READ_WRITE_DEADLINE) // 6 seconds
			for _, c := range conns {
				writeMu.Delete(c)
			}
		}(oldConns, guards.WriteMu)
	}

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
	defer func() {
		select {
		case <-qw.done:
		default:
			close(qw.done)
		}
	}()

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
	defer func() {
		select {
		case <-qw.done:
		default:
			close(qw.done)
		}
	}()

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
				} else {
					// Validation failed (e.g. NOT_READY) — remove from answered
					// so the node gets resent to on the next timer tick.
					qw.mu.Lock()
					delete(qw.answered, id)
					delete(qw.responses, id)
					qw.mu.Unlock()
				}
			}(r.id, r.msg)

		case <-validCh:
			// Check if we reached majority of validated responses
			validMu.Lock()
			validCount := len(validAnswered)
			validMu.Unlock()

			if validCount >= majority {
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

// getWriteMuConn returns a per-connection mutex.
// We never delete mutexes from the map to avoid the race condition that causes panic.
func (qw *QuorumWaiter) getWriteMuConn(c *websocket.Conn) *sync.Mutex {
	if c == nil {
		return &sync.Mutex{}
	}
	if m, ok := qw.guards.WriteMu.Load(c); ok {
		return m.(*sync.Mutex)
	}
	m := &sync.Mutex{}
	actual, _ := qw.guards.WriteMu.LoadOrStore(c, m)
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

func dialWebsocket(rawURL string) (*websocket.Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("dial error: %w", err)
	}
	return conn, nil
}

func openWebsocketConnectionWithPoD() (*websocket.Conn, error) {
	return dialWebsocket(globals.CONFIGURATION.PointOfDistributionWS)
}

func openWebsocketConnectionWithAnchorsPoD() (*websocket.Conn, error) {
	return dialWebsocket(globals.CONFIGURATION.AnchorsPointOfDistributionWS)
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
			wmu := qw.getWriteMuConn(c)
			wmu.Lock()
			_ = c.SetWriteDeadline(time.Now().Add(READ_WRITE_DEADLINE))
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
				_ = c.Close()
				delete(wsConnMap, id)
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
