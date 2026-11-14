package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"golang.org/x/crypto/bcrypt"
)

//go:embed starter-solutions.json
var starterSolutions []byte

type technicalCenter struct{ Code, LoginPrefix, Region string }

var technicalCenters = []technicalCenter{
	{"НБР", "nbr", "Ямало-Ненецкий АО"},
	{"АСБ", "asb", "Свердловская область"},
	{"ИРБ", "irb", "Свердловская область"},
	{"КРТ", "krt", "Свердловская область"},
	{"НТГ", "ntg", "Свердловская область"},
	{"НТР", "ntr", "Свердловская область"},
	{"НУРЛ", "nurl", "Свердловская область"},
	{"НВРТ", "nvrt", "Ханты-Мансийский АО — Югра"},
	{"НУРГ", "nurg", "Ямало-Ненецкий АО"},
	{"ОКТ", "okt", "Ханты-Мансийский АО — Югра"},
	{"СЛХ", "slh", "Ямало-Ненецкий АО"},
	{"СОВ", "sov", "Ханты-Мансийский АО — Югра"},
	{"СРГ", "srg", "Ханты-Мансийский АО — Югра"},
	{"ХМН", "hmn", "Ханты-Мансийский АО — Югра"},
	{"ЕКБ", "ekb", "Свердловская область"},
	{"КМУ", "kmu", "Свердловская область"},
	{"КРГ", "krg", "Курганская область"},
	{"НСР", "nsr", "Свердловская область"},
}

// Start with the current 12-hour shift in network wall time, including its start.
func firstShift(now time.Time) time.Time {
	local := now.In(networkZone)
	start := time.Date(local.Year(), local.Month(), local.Day(), 8, 0, 0, 0, networkZone)
	if local.Before(start) {
		return start.Add(-12 * time.Hour)
	}
	if !local.Before(start.Add(12 * time.Hour)) {
		return start.Add(12 * time.Hour)
	}
	return start
}

func (s *store) bootstrapInitialData(ctx context.Context, now time.Time) error {
	if os.Getenv("SVYAZI_NET_SEED_INITIAL_DATA") == "0" {
		return nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked int
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK('svyazi.net-starter-data',30)").Scan(&locked); err != nil {
		return err
	}
	if locked != 1 {
		return fmt.Errorf("не удалось получить блокировку начальных данных")
	}
	defer conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK('svyazi.net-starter-data')")
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS bootstrap_data (
 version VARCHAR(80) PRIMARY KEY, applied_at DATETIME NOT NULL
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		return err
	}
	var applied int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM bootstrap_data WHERE version='starter-v1'").Scan(&applied); err != nil {
		return err
	}
	if applied != 0 {
		return nil
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	passwordsMu.Lock()
	defer passwordsMu.Unlock()
	passwords, err := loadPasswords()
	if err != nil {
		return err
	}
	start := firstShift(now)
	engineersCreated, shiftsCreated := 0, 0
	for _, center := range technicalCenters {
		var people []engineer
		for number := 1; number <= 5; number++ {
			person := engineer{Login: fmt.Sprintf("%s.eng%02d", center.LoginPrefix, number), Name: fmt.Sprintf("Инженер %s %02d", center.Code, number), Region: center.Region, Center: center.Code, Role: "engineer"}
			var existingCenter, existingRole string
			err := tx.QueryRowContext(ctx, "SELECT id,center,role,name FROM svyazi_net_users.engineers WHERE login=?", person.Login).Scan(&person.ID, &existingCenter, &existingRole, &person.Name)
			if err == sql.ErrNoRows {
				person.Name = fmt.Sprintf("Инженер %s %02d", center.Code, number)
				password := passwords[person.Login]
				if password == "" || len(password) > 72 {
					raw := make([]byte, 16)
					if _, err := rand.Read(raw); err != nil {
						return err
					}
					password = hex.EncodeToString(raw)
				}
				hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
				if err != nil {
					return err
				}
				result, err := tx.ExecContext(ctx, "INSERT INTO svyazi_net_users.engineers (login,name,region,center,role,password_hash) VALUES (?,?,?,?,'engineer',?)", person.Login, person.Name, person.Region, person.Center, string(hash))
				if err != nil {
					return err
				}
				person.ID, err = result.LastInsertId()
				if err != nil {
					return err
				}
				passwords[person.Login] = password
				engineersCreated++
			} else if err != nil {
				return err
			} else if existingCenter != center.Code || existingRole != "engineer" {
				return fmt.Errorf("логин %s уже занят другой учёткой", person.Login)
			}
			people = append(people, person)
		}
		// Existing operator planning wins over the generated schedule for this center.
		var planned int
		err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM svyazi_net_users.duty_schedule d JOIN svyazi_net_users.engineers e ON e.id=d.engineer_id
 WHERE e.center=? AND d.shift_start<? AND d.shift_end>?`, center.Code, start.AddDate(0, 0, 30).Format(stamp), start.Format(stamp)).Scan(&planned)
		if err != nil {
			return err
		}
		if planned != 0 {
			continue
		}
		for shift := 0; shift < 60; shift++ {
			person := people[shift%len(people)]
			begin := start.Add(time.Duration(shift) * 12 * time.Hour)
			_, err := tx.ExecContext(ctx, `INSERT INTO svyazi_net_users.duty_schedule (engineer_id,operator_name,shift_start,shift_end,note) VALUES (?,?,?,?,?)`, person.ID, person.Name, begin.Format(stamp), begin.Add(12*time.Hour).Format(stamp), "Стартовый график ТЦ "+center.Code+". Уточнить у руководителя.")
			if err != nil {
				return err
			}
			shiftsCreated++
		}
	}
	var solutions []solutionDraft
	if err := json.Unmarshal(starterSolutions, &solutions); err != nil {
		return err
	}
	for _, entry := range solutions {
		if err := validateSolution(&entry); err != nil {
			return err
		}
		variants, err := json.Marshal(entry.Variants)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO solutions (title,symptoms,variants,author_name,center,created_at) VALUES (?,?,?,'Стартовая база знаний','',?)`, entry.Title, entry.Symptoms, string(variants), now.In(networkZone).Format(stamp))
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO bootstrap_data (version,applied_at) VALUES ('starter-v1',?)", now.In(networkZone).Format("2006-01-02 15:04:05")); err != nil {
		return err
	}
	// Save generated secrets before committing: a failed commit can be retried
	// with the same passwords, rather than losing access to newly created users.
	if err := savePasswordMap(passwords); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("Начальные данные: %d инженеров, %d смен, %d решений", engineersCreated, shiftsCreated, len(solutions))
	return nil
}
