package server

import (
	"context"
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Plaintext recovery/export is explicitly requested by the deployment owner.
// Keep it outside the HTTP asset allowlist and outside the database.
var passwordsMu sync.Mutex

func passwordDirectory() string {
	if path := os.Getenv("SVYAZI_NET_CREDENTIALS_DIR"); path != "" {
		return path
	}
	return filepath.Join(".local", "credentials")
}

func loadPasswords() (map[string]string, error) {
	result := map[string]string{}
	f, err := os.Open(filepath.Join(passwordDirectory(), "users-passwords.tsv"))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	reader := csv.NewReader(f)
	reader.Comma = '\t'
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return result, nil
	}
	for _, row := range rows[1:] {
		if len(row) == 4 {
			result[row[1]] = row[3]
		}
	}
	return result, nil
}

func writePasswords(rows [][]string) error {
	dir := passwordDirectory()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".users-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	writer := csv.NewWriter(f)
	writer.Comma = '\t'
	err = writer.WriteAll(rows)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "users-passwords.tsv"))
}

func rememberPassword(login, password string) error {
	passwordsMu.Lock()
	defer passwordsMu.Unlock()
	values, err := loadPasswords()
	if err != nil {
		return err
	}
	values[login] = password
	return savePasswordMap(values)
}

// Caller holds passwordsMu while replacing the credential file.
func savePasswordMap(values map[string]string) error {
	rows := [][]string{{"Роль", "Логин", "Имя", "Пароль"}}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		rows = append(rows, []string{"", key, "", values[key]})
	}
	return writePasswords(rows)
}

func (s *store) exportPasswords(ctx context.Context) error {
	passwordsMu.Lock()
	defer passwordsMu.Unlock()
	values, err := loadPasswords()
	if err != nil {
		return err
	}
	records, err := s.db.QueryContext(ctx, "SELECT role,login,name FROM svyazi_net_users.engineers ORDER BY role,login")
	if err != nil {
		return err
	}
	defer records.Close()
	rows := [][]string{{"Роль", "Логин", "Имя", "Пароль"}}
	for records.Next() {
		var role, login, name string
		if err := records.Scan(&role, &login, &name); err != nil {
			return err
		}
		password, known := values[login]
		if !known {
			password = "НЕИЗВЕСТЕН: учётка создана до включения экспорта"
		}
		rows = append(rows, []string{roles[role], login, name, password})
	}
	if err := records.Err(); err != nil {
		return err
	}
	return writePasswords(rows)
}
