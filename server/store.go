package server

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

//go:embed schema.sql
var schema string

type store struct{ db *sql.DB }

const fields = "PW,NAPR,TEHC,KATEG,CAT_LEGACY,NETW_TYPE,SERV_ELEM,NUMB_RBS,RBS_SECTOR,EQIP_TYPE,ALARM_TYPE,DESCR,BEGIN_TIME,END_TIME,AKB,DGU,LENGHT,PROSTOY,COMMON_PROSTOY,AKB_SECONDS,DGU_SECONDS,PROSTOY_SECONDS,COMMON_PROSTOY_SECONDS,ALARM_WHO,DESC_NOTICE"
const selected = "SELECT incidents.id,revision,PW,NAPR,TEHC,KATEG,CAT_LEGACY,NETW_TYPE,SERV_ELEM,NUMB_RBS,RBS_SECTOR,EQIP_TYPE,ALARM_TYPE,DESCR,CAST(BEGIN_TIME AS CHAR),CAST(END_TIME AS CHAR),AKB_SECONDS,DGU_SECONDS,PROSTOY_SECONDS,ALARM_WHO,DESC_NOTICE,COALESCE(engineers.id,0),COALESCE(engineers.name,'') FROM incidents LEFT JOIN svyazi_net_users.engineers ON engineers.id = incidents.engineer_id"

func openStore(ctx context.Context, seed []byte) (*store, error) {
	const engine, suffix = "AUTO_INCREMENT", "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	const conflict = "ON DUPLICATE KEY UPDATE seed_key = seed_key"
	port := os.Getenv("DB_PORT")
	if port == "" {
		port = "3306"
	}
	host := os.Getenv("DB_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	cfg := mysql.NewConfig()
	cfg.Net, cfg.Addr = "tcp", net.JoinHostPort(host, port)
	cfg.User, cfg.Passwd, cfg.DBName = os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"), "svyazi_net_incidents"
	if cfg.User == "" {
		cfg.User = "svyazi_net"
	}
	cfg.Collation = "utf8mb4_general_ci"
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 5*time.Second, 15*time.Second, 15*time.Second
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("не удалось подключиться к MariaDB по адресу %s: %w. Запустите проект командой sudo sh start.sh; настройки подключения читаются из .env", cfg.Addr, err)
	}
	success := false
	defer func() {
		if !success {
			_ = db.Close()
		}
	}()
	ddl := strings.NewReplacer("__AUTO_INCREMENT__", engine, "__TABLE_OPTIONS__", suffix).Replace(strings.TrimSpace(schema))
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return nil, fmt.Errorf("initialize schema: %w", err)
	}
	if err := initializeEngineers(ctx, db, engine, suffix); err != nil {
		return nil, fmt.Errorf("initialize assignments: %w", err)
	}
	if err := initializeShiftReports(ctx, db, engine, suffix); err != nil {
		return nil, fmt.Errorf("initialize shift reports: %w", err)
	}
	if err := initializeSolutions(ctx, db, engine, suffix); err != nil {
		return nil, fmt.Errorf("initialize solutions: %w", err)
	}
	if err := initializeDutySchedule(ctx, db, engine, suffix); err != nil {
		return nil, fmt.Errorf("initialize duty schedule: %w", err)
	}
	if err := importSeed(ctx, db, seed, conflict); err != nil {
		return nil, fmt.Errorf("import Excel seed: %w", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE incidents SET COMMON_PROSTOY = PROSTOY,
		COMMON_PROSTOY_SECONDS = PROSTOY_SECONDS
		WHERE RBS_SECTOR = 1 AND PROSTOY_SECONDS IS NOT NULL AND COMMON_PROSTOY_SECONDS IS NULL`); err != nil {
		return nil, fmt.Errorf("recalculate sector downtime: %w", err)
	}
	success = true
	return &store{db: db}, nil
}

func placeholders(n int) string { return strings.Repeat("?,", n-1) + "?" }

func importSeed(ctx context.Context, db *sql.DB, seed []byte, conflict string) error {
	var entries []struct {
		Key    string          `json:"seedKey"`
		Event  event           `json:"event"`
		Source json.RawMessage `json:"source"`
	}
	if err := json.Unmarshal(seed, &entries); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT seed_key FROM incidents WHERE seed_key IS NOT NULL")
	if err != nil {
		return err
	}
	seeded := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		seeded[key] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	query := "INSERT INTO incidents (seed_key,source_data," + fields + ") VALUES (" + placeholders(27) + ") " + conflict
	now := time.Now()
	for _, entry := range entries {
		if seeded[entry.Key] {
			continue
		}
		entry.Event.compute(now)
		args := append([]any{entry.Key, string(entry.Source)}, columnValues(entry.Event)...)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("%s: %w", entry.Key, err)
		}
	}
	return tx.Commit()
}

func columnValues(e event) []any {
	var category, legacy, stations any
	switch e.Category {
	case "1", "2", "3":
		category, _ = strconv.Atoi(e.Category)
	default:
		legacy = e.Category
	}
	if count, err := strconv.Atoi(e.Stations); err == nil {
		stations = count
	}
	sector := 0
	if e.Stations == "С" {
		sector = 1
	}
	end := zeroDate
	if e.End != "" {
		end = strings.Replace(e.End, "T", " ", 1)
	}
	sqlTime := func(seconds *int64) any {
		if seconds == nil || *seconds > maxSQLTime {
			return nil
		}
		return durationString(seconds)
	}
	return []any{e.Ticket, e.Direction, e.Center, category, legacy, e.Network, e.Element, stations, sector,
		e.Equipment, e.Type, e.Summary, strings.Replace(e.Start, "T", " ", 1), end, sqlTime(e.Battery), sqlTime(e.Generator),
		durationString(e.Duration), sqlTime(e.Outage), sqlTime(e.Total), e.Battery, e.Generator, e.Outage, e.Total, e.Notified, e.Details}
}

type scanner interface{ Scan(...any) error }

func readEvent(row scanner, now time.Time) (event, error) {
	var e event
	var category, legacy, stations sql.NullString
	var sector int
	err := row.Scan(&e.ID, &e.Revision, &e.Ticket, &e.Direction, &e.Center, &category, &legacy, &e.Network,
		&e.Element, &stations, &sector, &e.Equipment, &e.Type, &e.Summary, &e.Start, &e.End,
		&e.Battery, &e.Generator, &e.Outage, &e.Notified, &e.Details, &e.EngineerID, &e.EngineerName)
	if err != nil {
		return e, err
	}
	e.Category = legacy.String
	if category.Valid {
		e.Category = category.String
	}
	e.Stations = stations.String
	if sector != 0 {
		e.Stations = "С"
	}
	e.Start = strings.Replace(e.Start, " ", "T", 1)
	if e.End == zeroDate {
		e.End = ""
	} else {
		e.End = strings.Replace(e.End, " ", "T", 1)
	}
	e.compute(now)
	return e, nil
}

func (s *store) latest(ctx context.Context, today string, engineerID int64) (string, error) {
	var date sql.NullString
	query := "SELECT CAST(MAX(BEGIN_TIME) AS CHAR) FROM incidents"
	var args []any
	if engineerID != 0 {
		query += " WHERE engineer_id = ?"
		args = append(args, engineerID)
	}
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&date); err != nil {
		return "", err
	}
	if date.Valid && len(date.String) >= 10 {
		return date.String[:10], nil
	}
	return today, nil
}

func (s *store) list(ctx context.Context, now time.Time, from, to string, engineerID int64) ([]event, error) {
	today := now.In(networkZone).Format(dateOnly) + " 00:00:00"
	where := " WHERE END_TIME = ? OR END_TIME >= ? OR BEGIN_TIME >= ?"
	args := []any{zeroDate, today, today}
	if from != "" {
		where = " WHERE BEGIN_TIME >= ? AND BEGIN_TIME <= ?"
		args = []any{from + " 00:00:00", to + " 23:59:59"}
	}
	if engineerID != 0 {
		if from == "" {
			where, args = " WHERE incidents.engineer_id = ?", []any{engineerID}
		} else {
			where += " AND incidents.engineer_id = ?"
			args = append(args, engineerID)
		}
	}
	rows, err := s.db.QueryContext(ctx, selected+where+" ORDER BY BEGIN_TIME DESC,incidents.id DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []event{}
	for rows.Next() {
		e, err := readEvent(rows, now)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(events) > 0 {
		if err := s.markRepeated(ctx, events); err != nil {
			return nil, err
		}
	}
	return events, nil
}

func (s *store) save(ctx context.Context, id int64, d draft, now time.Time) (int64, error) {
	var previous *event
	if id != 0 {
		old, err := readEvent(s.db.QueryRowContext(ctx, selected+" WHERE incidents.id = ?", id), now)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, reject(404, "Авария не найдена.")
		}
		if err != nil {
			return 0, err
		}
		previous = &old
	}
	e, err := validate(d, previous, now)
	if err != nil {
		return 0, err
	}
	args := columnValues(e)
	var engineerID int64
	if previous != nil && previous.Center == e.Center {
		engineerID = previous.EngineerID
	}
	if d.EngineerID != nil {
		engineerID = *d.EngineerID
	}
	var assignment any
	if engineerID != 0 {
		var center string
		err := s.db.QueryRowContext(ctx, "SELECT center FROM svyazi_net_users.engineers WHERE id = ?", engineerID).Scan(&center)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if errors.Is(err, sql.ErrNoRows) || center != e.Center {
			return 0, reject(400, "Выберите инженера технического центра аварии.")
		}
		assignment = engineerID
	}
	args = append(args, assignment)
	if id == 0 {
		result, err := s.db.ExecContext(ctx, "INSERT INTO incidents ("+fields+",engineer_id) VALUES ("+placeholders(len(args))+")", args...)
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()
	}
	assignments := strings.ReplaceAll(fields, ",", " = ?,") + " = ?"
	result, err := s.db.ExecContext(ctx, "UPDATE incidents SET "+assignments+",engineer_id=?,revision=revision+1 WHERE id=? AND revision=?", append(args, id, d.Revision)...)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, reject(409, "Запись уже изменена. Обновите отчёт и откройте карточку снова.")
	}
	return id, nil
}
