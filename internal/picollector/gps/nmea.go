package gps

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

// Fix contains only a matching, checksum-verified RMC/GGA observation. HDOP is
// dimensionless geometry, not an accuracy estimate in meters.
type Fix struct {
	Latitude, Longitude     float64
	RecordedAt              time.Time
	SpeedKPH, CourseDegrees *float64
	AltitudeMeters          *float64
	Satellites              int
	HDOP                    float64
	Quality                 int
}

type rmc struct {
	at                  time.Time
	latitude, longitude float64
	speed, course       *float64
}
type gga struct {
	clock               string
	latitude, longitude float64
	altitude            *float64
	satellites, quality int
	hdop                float64
}
type Parser struct {
	r *rmc
	g *gga
}

var ErrSentence = errors.New("invalid GPS sentence")

// ValidSentence supports read-only baud detection even before a receiver has a fix.
func ValidSentence(line string) bool {
	f, e := sentence(line)
	return e == nil && len(f[0]) == 5 && (f[0][2:] == "RMC" || f[0][2:] == "GGA")
}

// Feed does not log raw sentences, locations or receiver identifiers.
func (p *Parser) Feed(line string, now time.Time) (*Fix, error) {
	fields, err := sentence(line)
	if err != nil {
		return nil, err
	}
	if len(fields[0]) != 5 {
		return nil, nil
	}
	switch fields[0][2:] {
	case "RMC":
		p.r = nil
		r, err := parseRMC(fields)
		if err != nil {
			return nil, err
		}
		// A synchronized host bounds receiver rollover/stale-output failures.
		if r.at.Before(now.Add(-24*time.Hour)) || r.at.After(now.Add(time.Minute)) {
			return nil, ErrSentence
		}
		p.r = r
	case "GGA":
		p.g = nil
		g, err := parseGGA(fields)
		if err != nil {
			return nil, err
		}
		p.g = g
	default:
		return nil, nil
	}
	if p.r == nil || p.g == nil || p.r.at.Format("150405.000000000") != p.g.clock {
		return nil, nil
	}
	r, g := p.r, p.g
	p.r, p.g = nil, nil
	if distance(r.latitude, r.longitude, g.latitude, g.longitude) > 15 {
		return nil, ErrSentence
	}
	return &Fix{Latitude: r.latitude, Longitude: r.longitude, RecordedAt: r.at, SpeedKPH: r.speed, CourseDegrees: r.course, AltitudeMeters: g.altitude, Satellites: g.satellites, HDOP: g.hdop, Quality: g.quality}, nil
}

func sentence(line string) ([]string, error) {
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if len(line) < 10 || len(line) > 256 || line[0] != '$' {
		return nil, ErrSentence
	}
	star := strings.LastIndexByte(line, '*')
	if star < 6 || star != len(line)-3 {
		return nil, ErrSentence
	}
	want, err := strconv.ParseUint(line[star+1:], 16, 8)
	if err != nil {
		return nil, ErrSentence
	}
	var sum byte
	for _, c := range []byte(line[1:star]) {
		if c < 32 || c > 126 {
			return nil, ErrSentence
		}
		sum ^= c
	}
	if sum != byte(want) {
		return nil, ErrSentence
	}
	return strings.Split(line[1:star], ","), nil
}

func number(s string, min, max float64) (float64, error) {
	v, e := strconv.ParseFloat(s, 64)
	if e != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < min || v > max {
		return 0, ErrSentence
	}
	return v, nil
}
func optional(s string, min, max float64) (*float64, error) {
	if s == "" {
		return nil, nil
	}
	v, e := number(s, min, max)
	return &v, e
}
func coordinate(s, hemisphere string, latitude bool) (float64, error) {
	width, max := 5, 180.0
	if latitude {
		width, max = 4, 90
	}
	whole := strings.Split(s, ".")[0]
	if len(whole) != width {
		return 0, ErrSentence
	}
	v, e := number(s, 0, max*100+59.999999)
	if e != nil {
		return 0, e
	}
	degrees := math.Floor(v / 100)
	minutes := v - degrees*100
	if minutes >= 60 || degrees+minutes/60 > max {
		return 0, ErrSentence
	}
	v = degrees + minutes/60
	if latitude {
		switch hemisphere {
		case "N":
		case "S":
			v = -v
		default:
			return 0, ErrSentence
		}
	} else {
		switch hemisphere {
		case "E":
		case "W":
			v = -v
		default:
			return 0, ErrSentence
		}
	}
	return v, nil
}
func clock(s string) (time.Time, error) {
	if len(s) < 6 || len(s) > 16 {
		return time.Time{}, ErrSentence
	}
	t, e := time.Parse("150405", s)
	if e != nil {
		return t, ErrSentence
	}
	return t, nil
}
func parseRMC(f []string) (*rmc, error) {
	if len(f) < 13 || f[2] != "A" || len(f[9]) != 6 {
		return nil, ErrSentence
	}
	// Older NMEA versions can mark dead reckoning as A with GGA quality 1.
	// Require the explicit GNSS mode rather than upgrading that ambiguity.
	if f[12] != "A" && f[12] != "D" && f[12] != "F" && f[12] != "R" && f[12] != "P" {
		return nil, ErrSentence
	}
	c, e := clock(f[1])
	if e != nil {
		return nil, e
	}
	d, e := time.Parse("020106", f[9])
	if e != nil {
		return nil, ErrSentence
	}
	at := time.Date(d.Year(), d.Month(), d.Day(), c.Hour(), c.Minute(), c.Second(), c.Nanosecond(), time.UTC)
	lat, e := coordinate(f[3], f[4], true)
	if e != nil {
		return nil, e
	}
	lon, e := coordinate(f[5], f[6], false)
	if e != nil {
		return nil, e
	}
	speed, e := optional(f[7], 0, 400/1.852)
	if e != nil {
		return nil, e
	}
	if speed != nil {
		*speed *= 1.852
	}
	course, e := optional(f[8], 0, 359.999999)
	if e != nil {
		return nil, e
	}
	return &rmc{at, lat, lon, speed, course}, nil
}
func parseGGA(f []string) (*gga, error) {
	if len(f) < 11 {
		return nil, ErrSentence
	}
	c, e := clock(f[1])
	if e != nil {
		return nil, e
	}
	lat, e := coordinate(f[2], f[3], true)
	if e != nil {
		return nil, e
	}
	lon, e := coordinate(f[4], f[5], false)
	if e != nil {
		return nil, e
	}
	quality, e := strconv.Atoi(f[6])
	if e != nil || (quality != 1 && quality != 2 && quality != 4 && quality != 5) {
		return nil, ErrSentence
	}
	sats, e := strconv.Atoi(f[7])
	if e != nil || sats < 3 || sats > 99 {
		return nil, ErrSentence
	}
	hdop, e := number(f[8], 0.01, 50)
	if e != nil {
		return nil, e
	}
	alt, e := optional(f[9], -1000, 20000)
	if e != nil || alt != nil && f[10] != "M" {
		return nil, ErrSentence
	}
	return &gga{c.Format("150405.000000000"), lat, lon, alt, sats, quality, hdop}, nil
}
func distance(a, b, c, d float64) float64 {
	r := math.Pi / 180
	x := math.Sin((c - a) * r / 2)
	y := math.Sin((d - b) * r / 2)
	h := x*x + math.Cos(a*r)*math.Cos(c*r)*y*y
	return 6371000 * 2 * math.Asin(math.Sqrt(math.Min(1, h)))
}
