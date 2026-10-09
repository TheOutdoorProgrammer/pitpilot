package receiverhistory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	bolt "go.etcd.io/bbolt"
)

const MaxRequestBytes = 32 << 20
const MaxEvents = 100000

type Snapshot struct {
	SHA256   string            `json:"sha256"`
	DeviceID string            `json:"deviceId"`
	Events   []json.RawMessage `json:"events"`
}

func HashFile(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", ErrInvalid
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 8192 || info.Size() > 256<<20 {
		return "", ErrInvalid
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(file, (256<<20)+1))
	if err != nil || n != info.Size() {
		return "", ErrInvalid
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadClosed never initializes or repairs a receiver database. The shared lock
// excludes a running bbolt writer; hashes additionally detect source replacement.
func ReadClosed(filename string) (snapshot Snapshot, err error) {
	snapshot.SHA256, err = HashFile(filename)
	if err != nil {
		return snapshot, err
	}
	db, err := bolt.Open(filename, 0600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return snapshot, ErrInvalid
	}
	defer db.Close()
	err = db.View(func(tx *bolt.Tx) error {
		invalid := false
		for checkErr := range tx.Check() {
			if checkErr != nil {
				invalid = true
			}
		}
		if invalid {
			return ErrInvalid
		}
		metadata, events := tx.Bucket([]byte("metadata")), tx.Bucket([]byte("events"))
		if metadata == nil || events == nil {
			return ErrInvalid
		}
		snapshot.DeviceID = string(metadata.Get([]byte("device_id")))
		if !Identifier.MatchString(snapshot.DeviceID) {
			return ErrInvalid
		}
		total := 0
		return events.ForEach(func(key, value []byte) error {
			total += len(value)
			if len(snapshot.Events) >= MaxEvents || total > MaxRequestBytes-1024 {
				return ErrInvalid
			}
			event, e := Parse(value)
			if e != nil || event.ID != string(key) || event.DeviceID != snapshot.DeviceID {
				return ErrInvalid
			}
			snapshot.Events = append(snapshot.Events, append(json.RawMessage(nil), value...))
			return nil
		})
	})
	if err != nil {
		return Snapshot{}, ErrInvalid
	}
	after, err := HashFile(filename)
	if err != nil || after != snapshot.SHA256 {
		return Snapshot{}, fmt.Errorf("receiver backup changed during reading")
	}
	if len(snapshot.Events) == 0 {
		return Snapshot{}, ErrInvalid
	}
	return snapshot, nil
}
