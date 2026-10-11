package picollector

import (
	"log/slog"
	"net/http"
	"time"
)

func deviceTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	// Canceling an HTTP/2 stream does not close its shared connection. Detect
	// a dead path after roaming even when requests keep timing out on it.
	t.HTTP2 = &http.HTTP2Config{
		SendPingTimeout:  10 * time.Second,
		PingTimeout:      5 * time.Second,
		WriteByteTimeout: 10 * time.Second,
		CountError: func(kind string) {
			if kind == "conn_close_lost_ping" {
				slog.Warn("collector transport connection lost", "protocol", "http2", "reason", "ping_timeout")
			}
		},
	}
	return t
}
