package picollector

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	bbolt "go.etcd.io/bbolt"
)

var ErrQueueFull = errors.New("durable queue is full")
var records = []byte("batches")
var metadata = []byte("metadata")
var pendingPi = []byte("pending_pi")
var pendingLegacy = []byte("pending_legacy")

type Queue struct {
	db    *bbolt.DB
	limit int
}
type Pending struct {
	Sequence uint64
	Batch    garage.SignalBatch
	Legacy   *LegacyEvent
	raw      []byte
}
type queueRecord struct {
	Batch  garage.SignalBatch `json:"batch"`
	Legacy *LegacyEvent       `json:"legacy,omitempty"`
}

func OpenQueue(dir, deviceID string, limit int, legacyID ...string) (*Queue, error) {
	if deviceID == "" || limit < 1 {
		return nil, errors.New("invalid queue configuration")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := bbolt.Open(filepath.Join(dir, "queue.db"), 0600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	q := &Queue{db, limit}
	err = db.Update(func(tx *bbolt.Tx) error {
		for _, bucket := range [][]byte{pendingPi, pendingLegacy} {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return err
			}
		}
		if _, err := tx.CreateBucketIfNotExists(records); err != nil {
			return err
		}
		m, err := tx.CreateBucketIfNotExists(metadata)
		if err != nil {
			return err
		}
		if old := m.Get([]byte("device")); old != nil && string(old) != deviceID {
			return errors.New("queue belongs to another device")
		}
		if old := m.Get([]byte("format")); old != nil && string(old) != "1" {
			return errors.New("unsupported queue format")
		}
		if err = m.Put([]byte("device"), []byte(deviceID)); err != nil {
			return err
		}
		legacy := ""
		if len(legacyID) > 0 {
			legacy = legacyID[0]
		}
		if old := m.Get([]byte("legacy")); old != nil && string(old) != legacy {
			return errors.New("queue legacy identity cannot change")
		}
		if err = m.Put([]byte("legacy"), []byte(legacy)); err != nil {
			return err
		}
		return m.Put([]byte("format"), []byte("1"))
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return q, nil
}
func (q *Queue) Close() error { return q.db.Close() }
func (q *Queue) Append(b garage.SignalBatch) error {
	return q.AppendDelivery(b, nil)
}
func (q *Queue) AppendDelivery(b garage.SignalBatch, legacy *LegacyEvent) error {
	if err := b.Validate(); err != nil {
		return err
	}
	full := false
	err := q.db.Update(func(tx *bbolt.Tx) error {
		bound := string(tx.Bucket(metadata).Get([]byte("legacy")))
		if (legacy == nil && bound != "") || (legacy != nil && (bound == "" || legacy.DeviceID != bound)) {
			return errors.New("legacy delivery must match queue enrollment")
		}
		bucket := tx.Bucket(records)
		if bucket.Stats().KeyN >= q.limit {
			full = true
			m := tx.Bucket(metadata)
			n := decodeUint(m.Get([]byte("rejected")))
			return m.Put([]byte("rejected"), encodeUint(n+1))
		}
		n, err := bucket.NextSequence()
		if err != nil {
			return err
		}
		if legacy != nil {
			cloned := *legacy
			cloned.Sequence = n
			legacy = &cloned
			if err = legacy.Validate(); err != nil {
				return err
			}
		}
		raw, err := json.Marshal(queueRecord{b, legacy})
		if err != nil {
			return err
		}
		key := encodeUint(n)
		var observed *time.Time
		for _, o := range b.Observations {
			if o.ObservedAt != nil && (observed == nil || o.ObservedAt.After(*observed)) {
				observed = o.ObservedAt
			}
		}
		for _, c := range b.Contexts {
			if c.ObservedAt != nil && (observed == nil || c.ObservedAt.After(*observed)) {
				observed = c.ObservedAt
			}
		}
		if observed != nil {
			if err = tx.Bucket(metadata).Put([]byte("last_observed"), []byte(observed.UTC().Format(time.RFC3339Nano))); err != nil {
				return err
			}
		}
		if err = tx.Bucket(pendingPi).Put(key, []byte{1}); err != nil {
			return err
		}
		if legacy != nil {
			if err = tx.Bucket(pendingLegacy).Put(key, []byte{1}); err != nil {
				return err
			}
		}
		return bucket.Put(key, raw)
	})
	if err == nil && full {
		return ErrQueueFull
	}
	return err
}
func (q *Queue) First() (p Pending, ok bool, err error) {
	return q.first(pendingPi)
}
func (q *Queue) FirstLegacy() (Pending, bool, error) { return q.first(pendingLegacy) }
func (q *Queue) first(index []byte) (p Pending, ok bool, err error) {
	err = q.db.View(func(tx *bbolt.Tx) error {
		k, _ := tx.Bucket(index).Cursor().First()
		if k == nil {
			return nil
		}
		p.Sequence = decodeUint(k)
		v := tx.Bucket(records).Get(k)
		p.raw = bytes.Clone(v)
		ok = true
		var r queueRecord
		if err := json.Unmarshal(v, &r); err != nil {
			return err
		}
		p.Batch = r.Batch
		p.Legacy = r.Legacy
		return nil
	})
	return
}
func (q *Queue) Ack(p Pending) error {
	return q.ack(p, pendingPi)
}
func (q *Queue) AckLegacy(p Pending) error { return q.ack(p, pendingLegacy) }
func (q *Queue) ack(p Pending, index []byte) error {
	return q.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(records)
		key := encodeUint(p.Sequence)
		if !bytes.Equal(b.Get(key), p.raw) {
			return errors.New("queue acknowledgement conflict")
		}
		if err := tx.Bucket(index).Delete(key); err != nil {
			return err
		}
		if bytes.Equal(index, pendingPi) {
			if err := tx.Bucket(metadata).Put([]byte("last_upload"), []byte(time.Now().UTC().Format(time.RFC3339Nano))); err != nil {
				return err
			}
		}
		if tx.Bucket(pendingPi).Get(key) == nil && tx.Bucket(pendingLegacy).Get(key) == nil {
			return b.Delete(key)
		}
		return nil
	})
}
func (q *Queue) Status() (count int, rejected uint64, err error) {
	err = q.db.View(func(tx *bbolt.Tx) error {
		count = tx.Bucket(records).Stats().KeyN
		rejected = decodeUint(tx.Bucket(metadata).Get([]byte("rejected")))
		return nil
	})
	return
}
func encodeUint(n uint64) []byte { b := make([]byte, 8); binary.BigEndian.PutUint64(b, n); return b }
func (q *Queue) Times() (observed, uploaded *time.Time, err error) {
	err = q.db.View(func(tx *bbolt.Tx) error {
		for _, v := range []struct {
			key    string
			target **time.Time
		}{{"last_observed", &observed}, {"last_upload", &uploaded}} {
			raw := tx.Bucket(metadata).Get([]byte(v.key))
			if raw == nil {
				continue
			}
			at, e := time.Parse(time.RFC3339Nano, string(raw))
			if e != nil {
				return errors.New("invalid queue timestamp")
			}
			*v.target = &at
		}
		return nil
	})
	return
}
func decodeUint(b []byte) uint64 {
	if len(b) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}
