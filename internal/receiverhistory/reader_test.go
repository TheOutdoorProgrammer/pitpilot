package receiverhistory

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func backupFixture(t *testing.T, mutate func(*bolt.Tx)) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "receiver.db")
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		metadata, e := tx.CreateBucket([]byte("metadata"))
		if e != nil {
			return e
		}
		if e = metadata.Put([]byte("device_id"), []byte("receiver")); e != nil {
			return e
		}
		events, e := tx.CreateBucket([]byte("events"))
		if e != nil {
			return e
		}
		at := time.Date(2026, 9, 24, 22, 6, 45, 687965828, time.UTC)
		raw, e := json.Marshal(Event{SchemaVersion: 1, ID: "event", DeviceID: "receiver", BootID: "boot", Sequence: 123, UptimeMS: 99, ObservedAt: &at, Readings: map[string]float64{"speed_kph": 25}})
		if e != nil {
			return e
		}
		if e = events.Put([]byte("event"), raw); e != nil {
			return e
		}
		if mutate != nil {
			mutate(tx)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestReadClosedPreservesOriginalAndRejectsWriter(t *testing.T) {
	path := backupFixture(t, nil)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadClosed(path)
	if err != nil || len(snapshot.Events) != 1 || len(snapshot.SHA256) != 64 {
		t.Fatalf("snapshot: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("source changed")
	}
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = ReadClosed(path); err == nil {
		t.Fatal("opened active writer")
	}
}
func TestReadClosedRejectsIdentityAndInvalidInput(t *testing.T) {
	for _, kind := range []string{"key", "device", "partial", "empty"} {
		t.Run(kind, func(t *testing.T) {
			path := backupFixture(t, func(tx *bolt.Tx) {
				events := tx.Bucket([]byte("events"))
				var err error
				switch kind {
				case "key":
					raw := bytes.Clone(events.Get([]byte("event")))
					err = events.Put([]byte("wrong-key"), raw)
				case "device":
					err = tx.Bucket([]byte("metadata")).Put([]byte("device_id"), []byte("other"))
				case "partial":
					err = events.Put([]byte("z-bad"), []byte(`{"schema_version":1}`))
				case "empty":
					err = events.Delete([]byte("event"))
				}
				if err != nil {
					t.Fatal(err)
				}
			})
			before, _ := os.ReadFile(path)
			if _, err := ReadClosed(path); err == nil {
				t.Fatal("accepted invalid source")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("modified rejected source")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "missing")
	if _, err := ReadClosed(path); err == nil {
		t.Fatal("accepted missing source")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created missing source")
	}
}
func TestParseRejectsAmbiguityUnknownMetricAndClock(t *testing.T) {
	valid := `{"schema_version":1,"id":"event","device_id":"receiver","boot_id":"boot","sequence":1,"uptime_ms":0,"observed_at":null,"readings":{"speed_kph":0},"dtcs":null}`
	if _, err := Parse([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{valid + valid, `{"schema_version":1,"schema_version":1}`, `{"schema_version":1,"id":"event","device_id":"receiver","boot_id":"boot","sequence":1,"uptime_ms":0,"observed_at":"1900-01-01T00:00:00Z","readings":{"speed_kph":0}}`, `{"schema_version":1,"id":"event","device_id":"receiver","boot_id":"boot","sequence":1,"uptime_ms":0,"readings":{"fake_metric":2}}`} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Fatal("accepted invalid raw event")
		}
	}
}
