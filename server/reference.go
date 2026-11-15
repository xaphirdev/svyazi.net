package server

import (
	"context"
	"strings"
)

type stationReference struct {
	Element   string `json:"element"`
	Center    string `json:"center"`
	Direction string `json:"direction"`
	Network   string `json:"network"`
}

func (s *store) stationReferences(ctx context.Context, engineerID int64) ([]stationReference, error) {
	query := "SELECT DISTINCT SERV_ELEM, TEHC, NAPR, NETW_TYPE FROM incidents WHERE SERV_ELEM <> ''"
	var args []any
	if engineerID != 0 {
		query += " AND engineer_id = ?"
		args = append(args, engineerID)
	}
	rows, err := s.db.QueryContext(ctx, query+" ORDER BY SERV_ELEM, TEHC, NAPR, NETW_TYPE", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []stationReference{}
	for rows.Next() {
		var station stationReference
		if err := rows.Scan(&station.Element, &station.Center, &station.Direction, &station.Network); err != nil {
			return nil, err
		}
		name := strings.TrimSpace(station.Element)
		if name != "" && name != "-" && name != "—" {
			result = append(result, station)
		}
	}
	return result, rows.Err()
}
