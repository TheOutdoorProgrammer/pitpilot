package gps

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func nmea(body string) string {
	var sum byte
	for _, c := range []byte(body) {
		sum ^= c
	}
	return fmt.Sprintf("$%s*%02X\r\n", body, sum)
}

const rmcFixture = "GPRMC,123519.00,A,4807.038,N,01131.000,E,22.4,84.4,091026,,,A"
const ggaFixture = "GPGGA,123519.00,4807.038,N,01131.000,E,1,08,0.9,545.4,M,46.9,M,,"

func TestMatchingFixBothSentenceOrders(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 35, 20, 0, time.UTC)
	for _, lines := range [][]string{{rmcFixture, ggaFixture}, {ggaFixture, rmcFixture}} {
		p := Parser{}
		if f, e := p.Feed(nmea(lines[0]), now); e != nil || f != nil {
			t.Fatal("incomplete observation accepted", e)
		}
		f, e := p.Feed(nmea(lines[1]), now)
		if e != nil || f == nil {
			t.Fatal(e)
		}
		if math.Abs(f.Latitude-48.1173) > 1e-8 || math.Abs(f.Longitude-11.5166666667) > 1e-8 || f.Quality != 1 || f.Satellites != 8 || f.HDOP != 0.9 || f.SpeedKPH == nil || math.Abs(*f.SpeedKPH-41.4848) > 1e-7 || f.AltitudeMeters == nil || *f.AltitudeMeters != 545.4 || f.RecordedAt.Format(time.RFC3339) != "2026-10-09T12:35:19Z" {
			t.Fatal("incorrect parsed fix")
		}
	}
}
func TestInvalidOrUnmatchedGPSNeverCreatesFix(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 35, 20, 0, time.UTC)
	for name, line := range map[string]string{
		"checksum":              strings.TrimSuffix(nmea(rmcFixture), "\r\n") + "00",
		"void":                  nmea(strings.Replace(rmcFixture, ",A,", ",V,", 1)),
		"simulated":             nmea(strings.TrimSuffix(rmcFixture, "A") + "S"),
		"missing explicit mode": nmea(strings.TrimSuffix(rmcFixture, ",A")),
		"empty explicit mode":   nmea(strings.TrimSuffix(rmcFixture, "A")),
		"RMC dead reckoning":    nmea(strings.TrimSuffix(rmcFixture, "A") + "E"),
		"no fix":                nmea(strings.Replace(ggaFixture, ",1,08,", ",0,08,", 1)),
		"dead reckoning":        nmea(strings.Replace(ggaFixture, ",1,08,", ",6,08,", 1)),
		"bad minutes":           nmea(strings.Replace(rmcFixture, "4807.038", "4861.000", 1)),
		"bad latitude":          nmea(strings.Replace(rmcFixture, "4807.038", "9100.000", 1)),
		"nan":                   nmea(strings.Replace(ggaFixture, ",0.9,", ",NaN,", 1)),
		"stale rollover":        nmea(strings.Replace(rmcFixture, "091026", "090406", 1)),
		"no date":               nmea(strings.Replace(rmcFixture, "091026", "", 1)),
		"low satellites":        nmea(strings.Replace(ggaFixture, ",08,", ",02,", 1)),
		"wrong altitude units":  nmea(strings.Replace(ggaFixture, "545.4,M", "545.4,F", 1)),
	} {
		t.Run(name, func(t *testing.T) {
			p := Parser{}
			f, e := p.Feed(line, now)
			if f != nil || e == nil {
				t.Fatal("accepted invalid fix")
			}
		})
	}
	p := Parser{}
	p.Feed(nmea(rmcFixture), now)
	if f, e := p.Feed(nmea(strings.Replace(ggaFixture, "123519.00", "123520.00", 1)), now); e != nil || f != nil {
		t.Fatal("joined mismatched timestamps")
	}
}
func TestRealZeroCoordinateAndMidnightRemainValid(t *testing.T) {
	p := Parser{}
	now := time.Date(2026, 10, 10, 0, 0, 1, 0, time.UTC)
	p.Feed(nmea("GNGGA,000000.00,0000.000,N,00000.000,E,2,10,1.1,-10.0,M,0.0,M,,"), now)
	f, e := p.Feed(nmea("GNRMC,000000.00,A,0000.000,N,00000.000,E,0.0,,101026,,,D"), now)
	if e != nil || f == nil || f.Latitude != 0 || f.Longitude != 0 || !f.RecordedAt.Equal(now.Add(-time.Second)) || f.CourseDegrees != nil {
		t.Fatal("valid midnight/zero fix rejected", e)
	}
}
