package picollector

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TheOutdoorProgrammer/pitpilot/internal/garage"
	obd "github.com/TheOutdoorProgrammer/pitpilot/internal/picollector/obd"
)

func testBatch(t *testing.T, id string) garage.SignalBatch {
	t.Helper()
	b, err := Batch(obd.Observation{Readings: map[string]float64{"rpm": 750}, DTCs: []string{}}, time.Now().UTC(), id)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestQueueDurableIdentityCapacityAndACK(t *testing.T) {
	dir := t.TempDir()
	q, err := OpenQueue(dir, "device-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Append(testBatch(t, "a")); err != nil {
		t.Fatal(err)
	}
	if err = q.Append(testBatch(t, "b")); !errors.Is(err, ErrQueueFull) {
		t.Fatal(err)
	}
	p, ok, err := q.First()
	if err != nil || !ok || p.Sequence != 1 {
		t.Fatal(p, ok, err)
	}
	if n, rejected, err := q.Status(); err != nil || n != 1 || rejected != 1 {
		t.Fatal(n, rejected, err)
	}
	q.Close()
	if q, err = OpenQueue(dir, "different", 1); err == nil {
		q.Close()
		t.Fatal("accepted wrong device")
	}
	q, err = OpenQueue(dir, "device-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	again, _, _ := q.First()
	if string(again.raw) != string(p.raw) {
		t.Fatal("retry changed batch")
	}
	bad := p
	bad.raw = []byte("mismatch")
	if q.Ack(bad) == nil {
		t.Fatal("accepted wrong ACK")
	}
	if err = q.Ack(p); err != nil {
		t.Fatal(err)
	}
	if err = q.Append(testBatch(t, "b")); err != nil {
		t.Fatal(err)
	}
	next, _, _ := q.First()
	if next.Sequence != 2 {
		t.Fatal("sequence reset")
	}
	info, _ := os.Stat(filepath.Join(dir, "queue.db"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("queue not private")
	}
}
func TestTwoSinksAdvanceIndependentlyAndKeepRecordUntilBothACK(t *testing.T) {
	dir := t.TempDir()
	q, err := OpenQueue(dir, "pi", 10, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		b := testBatch(t, id)
		event := &LegacyEvent{SchemaVersion: 1, ID: id, DeviceID: "legacy", BootID: "new-session", Readings: map[string]float64{"rpm": 750}}
		if err = q.AppendDelivery(b, event); err != nil {
			t.Fatal(err)
		}
	}
	p, _, _ := q.First()
	l, _, _ := q.FirstLegacy()
	if p.Sequence != 1 || l.Legacy.Sequence != 1 {
		t.Fatal("missing durable identity")
	}
	if err = q.AckLegacy(l); err != nil {
		t.Fatal(err)
	}
	next, _, _ := q.FirstLegacy()
	if next.Sequence != 2 {
		t.Fatal("healthy sink blocked behind offline sink")
	}
	if n, _, _ := q.Status(); n != 2 {
		t.Fatal("record deleted before both ACKs")
	}
	q.Close()
	q, err = OpenQueue(dir, "pi", 10, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	next, _, _ = q.FirstLegacy()
	if next.Sequence != 2 {
		t.Fatal("legacy cursor was not durable")
	}
	if err = q.Ack(p); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := q.Status(); n != 1 {
		t.Fatal("both ACKs did not release record")
	}
	if err = q.Append(testBatch(t, "c")); err == nil {
		t.Fatal("silently disabled legacy delivery")
	}
}
func TestBatchDiagnosticUnknownIsNotClear(t *testing.T) {
	b := testBatch(t, "sample")
	if b.Contexts[0].Diagnostic.Unknown || b.Contexts[0].Diagnostic.Codes == nil {
		t.Fatal("lost confirmed clear state")
	}
	if !b.Contexts[1].Diagnostic.Unknown || b.Contexts[1].Diagnostic.SuccessfulReads != 0 {
		t.Fatal("fabricated pending diagnostic result")
	}
	if _, err := Batch(obd.Observation{Readings: map[string]float64{"rpm": 1}}, time.Time{}, "bad-clock"); err == nil {
		t.Fatal("accepted unknown clock as dated sample")
	}
}

func TestEmptyUnknownObservationNeverPoisonsEitherSink(t *testing.T) {
	if _, err := Batch(obd.Observation{}, time.Now().UTC(), "empty"); err == nil {
		t.Fatal("invented unknown-only contexts for empty observation")
	}
	if _, err := Batch(obd.Observation{DTCs: []string{}}, time.Now().UTC(), "clear"); err != nil {
		t.Fatal("lost explicit clear diagnostic observation", err)
	}
	q, err := OpenQueue(t.TempDir(), "pi", 10, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	e := LegacyEvent{SchemaVersion: 1, ID: "event", DeviceID: "legacy", BootID: "session", Sequence: 1}
	if e.Validate() == nil {
		t.Fatal("receiver contract accepted empty observation")
	}
	if q.AppendDelivery(testBatch(t, "event"), &e) == nil {
		t.Fatal("queued event rejected by legacy receiver")
	}
	if n, _, _ := q.Status(); n != 0 {
		t.Fatal("partial dual delivery persisted")
	}
	e.DTCs = []string{}
	if err = e.Validate(); err != nil {
		t.Fatal(err)
	}
	if err = q.AppendDelivery(testBatch(t, "event"), &e); err != nil {
		t.Fatal(err)
	}
	p, _, _ := q.FirstLegacy()
	if p.Sequence != 1 || p.Legacy.Sequence != 1 {
		t.Fatal("failed transaction advanced sequence")
	}
}

func TestObservationAndUploadTimestampsAreDurableAndSinkSpecific(t *testing.T) {
	dir := t.TempDir()
	q, err := OpenQueue(dir, "pi", 10, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if a, b, e := q.Times(); e != nil || a != nil || b != nil {
		t.Fatal("empty queue fabricated timestamps")
	}
	b := testBatch(t, "event")
	e := &LegacyEvent{SchemaVersion: 1, ID: "event", DeviceID: "legacy", BootID: "session", DTCs: []string{}}
	if err = q.AppendDelivery(b, e); err != nil {
		t.Fatal(err)
	}
	legacy, _, _ := q.FirstLegacy()
	q.AckLegacy(legacy)
	if at, uploaded, e := q.Times(); e != nil || at == nil || !at.Equal(*b.Observations[0].ObservedAt) || uploaded != nil {
		t.Fatal("legacy ACK became a PitPilot upload timestamp")
	}
	pi, _, _ := q.First()
	q.Ack(pi)
	q.Close()
	q, err = OpenQueue(dir, "pi", 10, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if observed, uploaded, e := q.Times(); e != nil || observed == nil || uploaded == nil {
		t.Fatal("timestamps did not survive reopen")
	}
}
