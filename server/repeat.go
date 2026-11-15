package server

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const repeatWindow = 30 * 24 * time.Hour
const repeatThreshold = 3

var placeSeparator = regexp.MustCompile(`\s+[—–-]\s+`)

func locationKey(center, direction, element string) string {
	// Use the whole label: the workbook reuses some prefixes for different stations.
	normalized := strings.ToLower(strings.Join(strings.Fields(element), " "))
	if normalized == "" || normalized == "-" || normalized == "—" {
		return ""
	}
	normalized = placeSeparator.ReplaceAllString(normalized, " — ")
	area := strings.TrimSpace(center)
	if area == "" {
		area = strings.TrimSpace(direction)
	}
	return strings.ToUpper(area) + "\x00" + normalized
}

func (s *store) markRepeated(ctx context.Context, events []event) error {
	// The list is sorted by start date descending, so a single bounded history
	// query covers all visible events, including those outside the report range.
	first, err := time.ParseInLocation(stamp, events[len(events)-1].Start, networkZone)
	if err != nil {
		return fmt.Errorf("parse accident start for recurrence: %w", err)
	}
	minStart := first.Add(-repeatWindow).Format("2006-01-02 15:04:05")
	maxStart := strings.Replace(events[0].Start, "T", " ", 1)
	rows, err := s.db.QueryContext(ctx, `SELECT TEHC,NAPR,SERV_ELEM,CAST(BEGIN_TIME AS CHAR)
  FROM incidents WHERE BEGIN_TIME >= ? AND BEGIN_TIME <= ? ORDER BY BEGIN_TIME`, minStart, maxStart)
	if err != nil {
		return err
	}
	defer rows.Close()

	history := make(map[string][]string)
	for rows.Next() {
		var center, direction, element, start string
		if err := rows.Scan(&center, &direction, &element, &start); err != nil {
			return err
		}
		if key := locationKey(center, direction, element); key != "" {
			history[key] = append(history[key], start)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range events {
		e := &events[i]
		starts := history[locationKey(e.Center, e.Direction, e.Element)]
		if len(starts) < repeatThreshold {
			continue
		}
		start, err := time.ParseInLocation(stamp, e.Start, networkZone)
		if err != nil {
			return err
		}
		earliest := start.Add(-repeatWindow).Format("2006-01-02 15:04:05")
		latest := start.Format("2006-01-02 15:04:05")
		lower := sort.SearchStrings(starts, earliest)
		upper := sort.Search(len(starts), func(j int) bool { return starts[j] > latest })
		if count := upper - lower; count >= repeatThreshold {
			e.RepeatCount = count
		}
	}
	return nil
}
