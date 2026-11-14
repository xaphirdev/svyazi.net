package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
)

type dutyAssignment struct {
	Center     string `json:"center"`
	Region     string `json:"region"`
	EngineerID int64  `json:"engineerId"`
	ID         int64  `json:"id"`
	Revision   int64  `json:"revision"`
	Operator   string `json:"operator"`
	Start      string `json:"start"`
	End        string `json:"end"`
	Note       string `json:"note"`
}

func initializeDutySchedule(ctx context.Context, db *sql.DB, engine, suffix string) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS svyazi_net_users.duty_schedule (
 id INTEGER PRIMARY KEY `+engine+`,
 revision INTEGER NOT NULL DEFAULT 1,
 engineer_id INTEGER NOT NULL,
 operator_name VARCHAR(120) NOT NULL,
 shift_start VARCHAR(19) NOT NULL,
 shift_end VARCHAR(19) NOT NULL,
 note VARCHAR(500) NOT NULL,
 INDEX idx_engineer_shift (engineer_id, shift_start, shift_end),
 FOREIGN KEY (engineer_id) REFERENCES svyazi_net_users.engineers(id),
 UNIQUE (operator_name, shift_start, shift_end)
 ) `+suffix)
	return err
}

func (s *store) dutySchedule(ctx context.Context) ([]dutyAssignment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.center,e.region,d.engineer_id,d.id,d.revision,d.operator_name,d.shift_start,d.shift_end,d.note
 FROM svyazi_net_users.duty_schedule d JOIN svyazi_net_users.engineers e ON e.id=d.engineer_id ORDER BY d.shift_start,e.center,d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []dutyAssignment{}
	for rows.Next() {
		var shift dutyAssignment
		if err := rows.Scan(&shift.Center, &shift.Region, &shift.EngineerID, &shift.ID, &shift.Revision, &shift.Operator, &shift.Start, &shift.End, &shift.Note); err != nil {
			return nil, err
		}
		result = append(result, shift)
	}
	return result, rows.Err()
}

func (s *store) saveDutyAssignment(ctx context.Context, id int64, shift dutyAssignment) (dutyAssignment, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return shift, err
	}
	defer tx.Rollback()
	// Lock the engineer to serialize concurrent overlapping assignments.
	err = tx.QueryRowContext(ctx, "SELECT name,center,region FROM svyazi_net_users.engineers WHERE id=? AND role='engineer' FOR UPDATE", shift.EngineerID).Scan(&shift.Operator, &shift.Center, &shift.Region)
	if errors.Is(err, sql.ErrNoRows) {
		return shift, reject(400, "Выберите инженера из справочника.")
	}
	if err != nil {
		return shift, err
	}
	shift.Operator, shift.Note = strings.TrimSpace(shift.Operator), strings.TrimSpace(shift.Note)
	shift.Start, shift.End = normalizeStamp(shift.Start), normalizeStamp(shift.End)
	if shift.Operator == "" || utf8.RuneCountInString(shift.Operator) > 120 || utf8.RuneCountInString(shift.Note) > 500 {
		return shift, reject(400, "Укажите ФИО дежурного (до 120 символов). Примечание — до 500 символов.")
	}
	if shift.Start == "" || shift.End == "" || shift.End <= shift.Start {
		return shift, reject(400, "Укажите период дежурства. Окончание должно быть позже начала.")
	}
	var result sql.Result
	var overlaps int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM svyazi_net_users.duty_schedule WHERE engineer_id=? AND id<>? AND shift_start<? AND shift_end>?`, shift.EngineerID, id, shift.End, shift.Start).Scan(&overlaps); err != nil {
		return shift, err
	}
	if overlaps != 0 {
		return shift, reject(409, "У инженера уже есть дежурство в это время.")
	}
	if id == 0 {
		result, err = tx.ExecContext(ctx, "INSERT INTO svyazi_net_users.duty_schedule (engineer_id,operator_name,shift_start,shift_end,note) VALUES (?,?,?,?,?)", shift.EngineerID, shift.Operator, shift.Start, shift.End, shift.Note)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE svyazi_net_users.duty_schedule SET engineer_id=?,operator_name=?,shift_start=?,shift_end=?,note=?,revision=revision+1
  WHERE id=? AND revision=?`, shift.EngineerID, shift.Operator, shift.Start, shift.End, shift.Note, id, shift.Revision)
	}
	if err != nil {
		var maria *mysql.MySQLError
		if errors.As(err, &maria) && maria.Number == 1062 {
			return shift, reject(409, "Такое дежурство уже есть в графике.")
		}
		return shift, err
	}
	if id == 0 {
		shift.ID, err = result.LastInsertId()
		shift.Revision = 1
		if err != nil {
			return shift, err
		}
		return shift, tx.Commit()
	}
	if err := checkDutyChange(result); err != nil {
		return shift, err
	}
	shift.ID = id
	shift.Revision++
	return shift, tx.Commit()
}

func checkDutyChange(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return reject(409, "Дежурство изменено или удалено. Обновите график.")
	}
	return nil
}

func (a *api) schedule(w http.ResponseWriter, r *http.Request, role string) error {
	ctx := r.Context()
	if r.URL.Path == "/api/schedule" {
		if r.Method == "GET" {
			shifts, err := a.store.dutySchedule(ctx)
			if err != nil {
				return err
			}
			jsonResponse(w, http.StatusOK, shifts)
			return nil
		}
		if r.Method != "POST" {
			return reject(405, "Метод не поддерживается.")
		}
		if role != "admin" {
			return reject(403, "Изменять график может администратор.")
		}
		var shift dutyAssignment
		if err := decodeBody(w, r, &shift); err != nil {
			return err
		}
		saved, err := a.store.saveDutyAssignment(ctx, 0, shift)
		if err != nil {
			return err
		}
		jsonResponse(w, http.StatusCreated, saved)
		return nil
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/schedule/"), 10, 64)
	if err != nil || id <= 0 {
		return reject(404, "Дежурство не найдено.")
	}
	if r.Method != "PUT" && r.Method != "DELETE" {
		return reject(405, "Метод не поддерживается.")
	}
	if role != "admin" {
		return reject(403, "Изменять график может администратор.")
	}
	var shift dutyAssignment
	if err := decodeBody(w, r, &shift); err != nil {
		return err
	}
	if r.Method == "DELETE" {
		result, err := a.store.db.ExecContext(ctx, "DELETE FROM svyazi_net_users.duty_schedule WHERE id=? AND revision=?", id, shift.Revision)
		if err != nil {
			return err
		}
		if err := checkDutyChange(result); err != nil {
			return err
		}
		jsonResponse(w, http.StatusOK, map[string]int64{"id": id})
		return nil
	}
	saved, err := a.store.saveDutyAssignment(ctx, id, shift)
	if err != nil {
		return err
	}
	jsonResponse(w, http.StatusOK, saved)
	return nil
}
