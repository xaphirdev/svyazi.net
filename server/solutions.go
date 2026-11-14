package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type solutionVariant struct {
	Condition    string `json:"condition"`
	Checks       string `json:"checks"`
	Repair       string `json:"repair"`
	Verification string `json:"verification"`
}

type solutionDraft struct {
	Station    string            `json:"station"`
	RepairedAt string            `json:"repairedAt"`
	Title      string            `json:"title"`
	Symptoms   string            `json:"symptoms"`
	Variants   []solutionVariant `json:"variants"`
}

type solution struct {
	EngineerID int64 `json:"engineerId"`
	solutionDraft
	ID        int64  `json:"id"`
	Author    string `json:"author"`
	Center    string `json:"center"`
	CreatedAt string `json:"createdAt"`
}

func initializeSolutions(ctx context.Context, db *sql.DB, engine, suffix string) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS solutions (
 id INTEGER PRIMARY KEY `+engine+`,
 title VARCHAR(160) NOT NULL,
 symptoms TEXT NOT NULL,
 variants TEXT NOT NULL,
 author_name VARCHAR(120) NOT NULL,
 center VARCHAR(40) NOT NULL,
 created_at VARCHAR(19) NOT NULL
 ) `+suffix)
	if err != nil {
		return err
	}
	// Upgrade existing installations without changing their published solutions.
	for _, column := range []struct{ name, definition string }{
		{"station", "VARCHAR(450) NOT NULL DEFAULT ''"},
		{"repaired_at", "VARCHAR(19) NOT NULL DEFAULT ''"},
		{"engineer_id", "INTEGER"},
	} {
		var exists int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='solutions' AND COLUMN_NAME=?", column.name).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, err := db.ExecContext(ctx, "ALTER TABLE solutions ADD COLUMN "+column.name+" "+column.definition); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSolution(d *solutionDraft) error {
	d.Title = strings.TrimSpace(d.Title)
	d.Symptoms = strings.TrimSpace(d.Symptoms)
	if d.Title == "" || utf8.RuneCountInString(d.Title) > 160 || d.Symptoms == "" || utf8.RuneCountInString(d.Symptoms) > 2000 {
		return reject(400, "Укажите название (до 160 символов) и симптомы (до 2000 символов).")
	}
	if len(d.Variants) < 1 || len(d.Variants) > 4 {
		return reject(400, "Добавьте от одного до четырёх вариантов решения.")
	}
	for i := range d.Variants {
		v := &d.Variants[i]
		v.Condition = strings.TrimSpace(v.Condition)
		v.Checks = strings.TrimSpace(v.Checks)
		v.Repair = strings.TrimSpace(v.Repair)
		v.Verification = strings.TrimSpace(v.Verification)
		if v.Checks == "" || v.Repair == "" {
			return reject(400, "Для каждого варианта укажите проверку и способ устранения.")
		}
		for _, value := range []string{v.Condition, v.Checks, v.Repair, v.Verification} {
			if utf8.RuneCountInString(value) > 1000 {
				return reject(400, "В каждом поле варианта допускается до 1000 символов.")
			}
		}
	}
	return nil
}

func (s *store) listSolutions(ctx context.Context) ([]solution, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,title,symptoms,variants,author_name,center,created_at,station,repaired_at,COALESCE(engineer_id,0)
 FROM solutions ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []solution{}
	for rows.Next() {
		var entry solution
		var variants []byte
		if err := rows.Scan(&entry.ID, &entry.Title, &entry.Symptoms, &variants, &entry.Author, &entry.Center, &entry.CreatedAt, &entry.Station, &entry.RepairedAt, &entry.EngineerID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(variants, &entry.Variants); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *store) addSolution(ctx context.Context, draft solutionDraft, identity session, now time.Time) (solution, error) {
	entry := solution{solutionDraft: draft, Author: roles[identity.role], CreatedAt: now.In(networkZone).Format(stamp)}
	if identity.engineer.ID != 0 {
		entry.Author, entry.Center = identity.engineer.Name, identity.engineer.Center
		entry.EngineerID = identity.engineer.ID
	}
	if err := validateSolution(&entry.solutionDraft); err != nil {
		return entry, err
	}
	entry.Station = strings.TrimSpace(entry.Station)
	if entry.Station == "" || utf8.RuneCountInString(entry.Station) > 450 {
		return entry, reject(400, "Укажите станцию, на которой выполнен ремонт (до 450 символов).")
	}
	entry.RepairedAt = normalizeStamp(entry.RepairedAt)
	if entry.RepairedAt == "" || entry.RepairedAt > now.In(networkZone).Format(stamp) {
		return entry, reject(400, "Укажите дату и время выполненного ремонта, не позже текущего времени.")
	}
	variants, err := json.Marshal(entry.Variants)
	if err != nil {
		return entry, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO solutions (title,symptoms,variants,author_name,center,created_at,station,repaired_at,engineer_id)
 VALUES (?,?,?,?,?,?,?,?,?)`, entry.Title, entry.Symptoms, string(variants), entry.Author, entry.Center, entry.CreatedAt, entry.Station, entry.RepairedAt, entry.EngineerID)
	if err != nil {
		return entry, err
	}
	entry.ID, err = result.LastInsertId()
	return entry, err
}

func (a *api) solutions(w http.ResponseWriter, r *http.Request, identity session, now time.Time) error {
	switch r.Method {
	case "GET":
		entries, err := a.store.listSolutions(r.Context())
		if err != nil {
			return err
		}
		jsonResponse(w, http.StatusOK, entries)
	case "POST":
		if identity.role != "engineer" {
			return reject(403, "Добавлять решения могут инженеры.")
		}
		var draft solutionDraft
		if err := decodeBodyLimit(w, r, &draft, 256*1024); err != nil {
			return err
		}
		entry, err := a.store.addSolution(r.Context(), draft, identity, now)
		if err != nil {
			return err
		}
		jsonResponse(w, http.StatusCreated, entry)
	default:
		w.Header().Set("Allow", "GET, POST")
		return reject(405, "Метод не поддерживается.")
	}
	return nil
}
