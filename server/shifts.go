package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
)

type shiftReport struct {
	ID            int64  `json:"id"`
	Operator      string `json:"operator"`
	Start         string `json:"start"`
	End           string `json:"end"`
	Summary       string `json:"summary"`
	OpenIncidents string `json:"openIncidents"`
	Handover      string `json:"handover"`
	SubmittedAt   string `json:"submittedAt"`
}

func initializeShiftReports(ctx context.Context, db *sql.DB, engine, suffix string) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS shift_reports (
  id INTEGER PRIMARY KEY `+engine+`,
  operator_name VARCHAR(120) NOT NULL,
  shift_start VARCHAR(19) NOT NULL,
  shift_end VARCHAR(19) NOT NULL,
  summary TEXT NOT NULL,
  open_incidents TEXT NOT NULL,
  handover TEXT NOT NULL,
  submitted_at VARCHAR(19) NOT NULL,
  UNIQUE (operator_name, shift_start, shift_end)
 ) `+suffix)
	return err
}

func (s *store) addShiftReport(ctx context.Context, report shiftReport, now time.Time) (shiftReport, error) {
	report.Operator = strings.TrimSpace(report.Operator)
	report.Start, report.End = normalizeStamp(report.Start), normalizeStamp(report.End)
	report.Summary = strings.TrimSpace(report.Summary)
	report.OpenIncidents = strings.TrimSpace(report.OpenIncidents)
	report.Handover = strings.TrimSpace(report.Handover)
	report.SubmittedAt = now.In(networkZone).Format(stamp)
	if report.Operator == "" || utf8.RuneCountInString(report.Operator) > 120 {
		return report, reject(400, "Укажите ФИО дежурного (до 120 символов).")
	}
	if report.Start == "" || report.End == "" || report.End <= report.Start || report.End > report.SubmittedAt {
		return report, reject(400, "Укажите прошедший период смены. Окончание должно быть позже начала.")
	}
	if report.Summary == "" {
		return report, reject(400, "Заполните итоги смены. Если событий не было, укажите это.")
	}
	for _, text := range []string{report.Summary, report.OpenIncidents, report.Handover} {
		if utf8.RuneCountInString(text) > 2000 {
			return report, reject(400, "Каждое поле отчёта может содержать до 2000 символов.")
		}
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO shift_reports
 (operator_name, shift_start, shift_end, summary, open_incidents, handover, submitted_at)
 VALUES (?,?,?,?,?,?,?)`, report.Operator, report.Start, report.End, report.Summary, report.OpenIncidents, report.Handover, report.SubmittedAt)
	if err != nil {
		var maria *mysql.MySQLError
		if errors.As(err, &maria) && maria.Number == 1062 {
			return report, reject(409, "Отчёт этого дежурного за указанный период уже сохранён.")
		}
		return report, err
	}
	report.ID, err = result.LastInsertId()
	return report, err
}

func (s *store) shiftReports(ctx context.Context) ([]shiftReport, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, operator_name, shift_start, shift_end,
 summary, open_incidents, handover, submitted_at FROM shift_reports ORDER BY shift_end DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reports := []shiftReport{}
	for rows.Next() {
		var r shiftReport
		if err := rows.Scan(&r.ID, &r.Operator, &r.Start, &r.End, &r.Summary, &r.OpenIncidents, &r.Handover, &r.SubmittedAt); err != nil {
			return nil, err
		}
		reports = append(reports, r)
	}
	return reports, rows.Err()
}

func (a *api) shifts(w http.ResponseWriter, r *http.Request, record session, now time.Time) error {
	role := record.role
	if role != "admin" && role != "duty" {
		return reject(403, "Отчёты смен доступны дежурному и администратору.")
	}
	switch r.Method {
	case "GET":
		reports, err := a.store.shiftReports(r.Context())
		if err != nil {
			return err
		}
		jsonResponse(w, http.StatusOK, reports)
		return nil
	case "POST":
		if role != "duty" {
			return reject(403, "Сдать отчёт может дежурный.")
		}
		var report shiftReport
		if err := decodeBody(w, r, &report); err != nil {
			return err
		}
		report.Operator = record.engineer.Name
		saved, err := a.store.addShiftReport(r.Context(), report, now)
		if err != nil {
			return err
		}
		jsonResponse(w, http.StatusCreated, saved)
		return nil
	default:
		w.Header().Set("Allow", "GET, POST")
		return reject(405, "Метод не поддерживается.")
	}
}
