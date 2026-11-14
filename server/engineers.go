package server

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

type engineer struct {
	ID     int64  `json:"id"`
	Login  string `json:"login"`
	Name   string `json:"name"`
	Region string `json:"region"`
	Role   string `json:"-"`
	Center string `json:"center"`
}

var engineerLogin = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,39}$`)

func initializeEngineers(ctx context.Context, db *sql.DB, engine, suffix string) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS svyazi_net_users.engineers (
 id INTEGER PRIMARY KEY `+engine+`,
 login VARCHAR(40) NOT NULL UNIQUE,
 name VARCHAR(120) NOT NULL,
 region VARCHAR(120) NOT NULL,
 role VARCHAR(16) NOT NULL DEFAULT 'engineer',
 center VARCHAR(40) NOT NULL,
 password_hash VARCHAR(100) NOT NULL
 ) `+suffix)
	if err != nil {
		return err
	}
	return nil
}

func (s *store) engineers(ctx context.Context) ([]engineer, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,login,name,region,role,center FROM svyazi_net_users.engineers WHERE role='engineer' ORDER BY region,center,name,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []engineer{}
	for rows.Next() {
		var person engineer
		if err := rows.Scan(&person.ID, &person.Login, &person.Name, &person.Region, &person.Role, &person.Center); err != nil {
			return nil, err
		}
		result = append(result, person)
	}
	return result, rows.Err()
}

func (s *store) addEngineer(ctx context.Context, person engineer, password string) (engineer, error) {
	person.Login = strings.ToLower(strings.TrimSpace(person.Login))
	person.Name = strings.TrimSpace(person.Name)
	person.Region = strings.TrimSpace(person.Region)
	_, reserved := roles[person.Login]
	if !engineerLogin.MatchString(person.Login) || reserved {
		return person, reject(400, "Укажите свободный логин: 3–40 латинских букв, цифр, точек, дефисов или подчёркиваний.")
	}
	if person.Region == "" || utf8.RuneCountInString(person.Region) > 120 || person.Name == "" || utf8.RuneCountInString(person.Name) > 120 || !slices.Contains(centers, person.Center) {
		return person, reject(400, "Укажите регион, имя инженера и выберите технический центр.")
	}
	if utf8.RuneCountInString(password) < 8 || len(password) > 72 {
		return person, reject(400, "Пароль должен содержать от 8 символов и занимать не более 72 байт.")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return person, err
	}
	result, err := s.db.ExecContext(ctx, "INSERT INTO svyazi_net_users.engineers (login,name,region,center,password_hash) VALUES (?,?,?,?,?)",
		person.Login, person.Name, person.Region, person.Center, string(hash))
	if err != nil {
		var maria *mysql.MySQLError
		if errors.As(err, &maria) && maria.Number == 1062 {
			return person, reject(409, "Этот логин уже занят.")
		}
		return person, err
	}
	person.ID, err = result.LastInsertId()
	if err != nil {
		return person, err
	}
	if err := rememberPassword(person.Login, password); err != nil {
		return person, err
	}
	if err := s.exportPasswords(ctx); err != nil {
		return person, err
	}
	return person, err
}

func (s *store) engineerCredentials(ctx context.Context, login string) (engineer, string, error) {
	var person engineer
	var hash string
	err := s.db.QueryRowContext(ctx, "SELECT id,login,name,region,role,center,password_hash FROM svyazi_net_users.engineers WHERE login = ?", login).
		Scan(&person.ID, &person.Login, &person.Name, &person.Region, &person.Role, &person.Center, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return person, "", nil
	}
	return person, hash, err
}
