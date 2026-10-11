package picollector

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Dropping writes models an established TCP connection whose old route no
// longer delivers packets, while subsequent dials can use the new route.
type blackholeConn struct {
	net.Conn
	drop   atomic.Bool
	once   sync.Once
	closed chan struct{}
}

func (c *blackholeConn) Write(p []byte) (int, error) {
	if c.drop.Load() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

func (c *blackholeConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.closed) })
	return err
}

func TestDeviceTransportRecoversHTTP2Blackhole(t *testing.T) {
	for _, healthChecks := range []bool{false, true} {
		name := "default_retains_dead_connection"
		if healthChecks {
			name = "health_check_redials"
		}
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			var slow atomic.Bool
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 2 || r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("expected authenticated HTTP/2")
				}
				requests.Add(1)
				if slow.Swap(false) {
					time.Sleep(250 * time.Millisecond)
				}
				json.NewEncoder(w).Encode(DeviceConfig{DeviceID: "device", VehicleID: "vehicle", ProtocolVersion: 1})
			}))
			server.EnableHTTP2 = true
			server.StartTLS()
			defer server.Close()
			client, err := NewClient(server.URL, "fixture", false)
			if err != nil {
				t.Fatal(err)
			}
			transport := client.http.Transport.(*http.Transport)
			defer transport.CloseIdleConnections()
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			transport.TLSClientConfig.RootCAs = roots
			transport.TLSClientConfig.InsecureSkipVerify = false
			transport.HTTP2.SendPingTimeout = 75 * time.Millisecond
			transport.HTTP2.PingTimeout = 75 * time.Millisecond
			if !healthChecks {
				transport.HTTP2 = nil
			}
			var first *blackholeConn
			var dials atomic.Int32
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				if dials.Add(1) == 1 {
					first = &blackholeConn{Conn: conn, closed: make(chan struct{})}
					return first, nil
				}
				return conn, nil
			}
			client.http.Timeout = time.Second
			if _, err := client.Configuration(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			slow.Store(true)
			if _, err := client.Configuration(context.Background()); err != nil {
				t.Fatal("healthy connection failed during a slow response", err)
			}
			if dials.Load() != 1 {
				t.Fatal("slow response discarded a healthy connection")
			}
			first.drop.Store(true)
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			_, err = client.Configuration(ctx)
			cancel()
			if err == nil {
				t.Fatal("blackholed request succeeded")
			}
			if !healthChecks {
				select {
				case <-first.closed:
					t.Fatal("baseline unexpectedly discarded dead connection")
				case <-time.After(250 * time.Millisecond):
				}
				if dials.Load() != 1 {
					t.Fatal("baseline unexpectedly redialed")
				}
				return
			}
			select {
			case <-first.closed:
			case <-time.After(2 * time.Second):
				t.Fatal("dead path was not closed")
			}
			if _, err := client.Configuration(context.Background()); err != nil {
				t.Fatal(err)
			}
			if dials.Load() != 2 || requests.Load() != 3 {
				t.Fatalf("dials=%d requests=%d; expected one fresh connection and no hidden request replay", dials.Load(), requests.Load())
			}
		})
	}
}

func TestDeviceTransportPreservesHTTPFailures(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "37")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.http.CloseIdleConnections()
	_, err = client.Configuration(context.Background())
	httpErr, ok := err.(*HTTPError)
	if !ok || httpErr.Status != http.StatusTooManyRequests || httpErr.RetryAfter != 37*time.Second || requests.Load() != 1 {
		t.Fatalf("rate limit or retry behavior changed: %v", err)
	}
}
