package server

import (
	"context"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"os"
	"time"
)

func (s *store) bootstrapAdmin(ctx context.Context) error {
	password := os.Getenv("admin.svyazi.net")
	if password == "" {
		password = "admin"
	}
	if len(password) > 72 {
		return fmt.Errorf("admin.svyazi.net: пароль должен занимать не более 72 байт")
	}
	for _, account := range []struct{ login, name, role, password string }{
		{"admin", "Администратор", "admin", password},
		{"duty", "Дежурный", "duty", "duty-local"},
	} {
		_, hash, err := s.engineerCredentials(ctx, account.login)
		if err != nil {
			return err
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(account.password)) != nil {
			encoded, err := bcrypt.GenerateFromPassword([]byte(account.password), bcrypt.DefaultCost)
			if err != nil {
				return err
			}
			hash = string(encoded)
		}
		_, err = s.db.ExecContext(ctx, `INSERT INTO svyazi_net_users.engineers (login,name,region,center,role,password_hash) VALUES (?,?,'','',?,?)
ON DUPLICATE KEY UPDATE name=VALUES(name),role=VALUES(role),password_hash=VALUES(password_hash)`, account.login, account.name, account.role, hash)
		if err != nil {
			return err
		}
		if err := rememberPassword(account.login, account.password); err != nil {
			return err
		}
	}
	return s.exportPasswords(ctx)
}

// Half-open intervals make consecutive shifts transfer rights at the exact boundary.
func (s *store) effectiveRole(ctx context.Context, engineerID int64, now time.Time) (string, error) {
	var count int
	stamp := now.In(networkZone).Format(stamp)
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM svyazi_net_users.duty_schedule
 WHERE engineer_id=? AND shift_start<=? AND shift_end>?`, engineerID, stamp, stamp).Scan(&count)
	if err != nil {
		return "", err
	}
	if count > 0 {
		return "duty", nil
	}
	return "engineer", nil
}
