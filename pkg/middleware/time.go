package middleware

import (
	"errors"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// ParseElasticsearchTime parses the time string supplied in the ES query. Returns:
// - Calculated time
// - The ES query parameter. For time format "now-X" this is just the string, for RFC 3339 format this is the UTC
//   time.Time.
func ParseElasticsearchTime(now time.Time, tstr *string) (*time.Time, interface{}, error) {
	if tstr == nil || *tstr == "" {
		return nil, nil, nil
	}
	clog := log.WithField("time", *tstr)
	// Expecting times in RFC3999 format, or now-<duration> format. Try the latter first.
	parts := strings.SplitN(*tstr, "-", 2)
	if strings.TrimSpace(parts[0]) == "now" {
		clog.Debug("Time is relative to now")

		// Make sure time is in UTC format.
		now = now.UTC()

		// Handle time string just being "now"
		if len(parts) == 1 {
			clog.Debug("Time is now")
			return &now, *tstr, nil
		}

		// Time string has section after the subtraction sign. We currently support minutes (m), hours (h) and days (d).
		clog.Debugf("Time string in now-x format; x=%s", parts[1])
		dur := strings.TrimSpace(parts[1])
		if dur == "0" {
			// 0 does not need units, so this also means now.
			clog.Debug("Zero delta - time is now")
			return &now, *tstr, nil
		} else if len(dur) < 2 {
			// We need at least two values for the unit and the value
			clog.Debug("Error parsing duration string, unrecognised unit of time")
			return nil, nil, errors.New("error parsing time in query - not a supported format")
		}

		// Last letter indicates the units.
		var mul time.Duration
		switch dur[len(dur)-1:] {
		case "m":
			mul = time.Minute
		case "h":
			mul = time.Hour
		case "d":
			// A day isn't necessarily 24hr, but this should be a good enough approximation for now.
			//TODO(rlb): If we really want to support the ES date math format then this'll need more work.
			mul = 24 * time.Hour
		default:
			clog.Debug("Error parsing duration string, unrecognised unit of time")
			return nil, nil, errors.New("error parsing time in query - not a supported format")
		}

		// First digits indicates the multiplier.
		if val, err := strconv.ParseUint(strings.TrimSpace(dur[:len(dur)-1]), 10, 64); err != nil {
			clog.WithError(err).Debug("Error parsing duration string")
			return nil, nil, err
		} else {
			t := now.Add(-(time.Duration(val) * mul))
			return &t, *tstr, nil
		}
	}

	// Not now-X format, parse as RFC3339.
	if t, err := time.Parse(time.RFC3339, *tstr); err == nil {
		clog.Debug("Time is in valid RFC3339 format")
		tutc := t.UTC()
		return &tutc, tutc.Unix(), nil
	} else {
		clog.Debug("Time format is not recognized")
		return nil, nil, err
	}
}
