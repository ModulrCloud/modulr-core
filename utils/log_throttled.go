package utils

import (
	"sync"
	"time"
)

const MAX_THROTTLED_LOG_KEYS = 10000

var THROTTLED_LOGS_MUTEX sync.Mutex
var THROTTLED_LOGS_LAST = make(map[string]time.Time)

// LogWithTimeThrottled logs at most once per `every` duration for a given `key`.
// This is meant for hot-path error logs (e.g., network retries) to avoid spamming stdout.
func LogWithTimeThrottled(key string, every time.Duration, msg, msgColor string) {
	if every <= 0 {
		LogWithTime(msg, msgColor)
		return
	}

	now := time.Now()

	THROTTLED_LOGS_MUTEX.Lock()
	if len(THROTTLED_LOGS_LAST) > MAX_THROTTLED_LOG_KEYS {
		// Safety valve to avoid unbounded growth if keys become highly dynamic.
		THROTTLED_LOGS_LAST = make(map[string]time.Time)
	}
	last, ok := THROTTLED_LOGS_LAST[key]
	if ok && now.Sub(last) < every {
		THROTTLED_LOGS_MUTEX.Unlock()
		return
	}
	THROTTLED_LOGS_LAST[key] = now
	THROTTLED_LOGS_MUTEX.Unlock()

	LogWithTime(msg, msgColor)
}
