package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Секрети — окрема таблиця з окремим доступом (довід у міграції 0053).
// Ключі названі тут, а не розкидані рядками по викликачах: назва секрету —
// це домовленість між пакетами api, tunnel і командою -reset-auth.
const (
	SecretPasswordHash  = "password_hash"
	SecretSessionSecret = "session_secret"
	SecretHATokenHash   = "ha_token_hash"

	SecretCFAPIToken    = "cf_api_token"
	SecretCFAccountID   = "cf_account_id"
	SecretCFZoneID      = "cf_zone_id"
	SecretCFTunnelID    = "cf_tunnel_id"
	SecretCFTunnelToken = "cf_tunnel_token"
	SecretCFHostname    = "cf_hostname"
	SecretCFDNSRecordID = "cf_dns_record_id"
	SecretCFLastError   = "cf_last_error"
)

// GetSecret — значення або порожньо, коли ключа немає (як GetSetting).
func (s *Store) GetSecret(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM secrets WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSecret — записати або замінити. Порожнє значення видаляє ключ: секрет
// без значення — це його відсутність, а не порожній секрет.
func (s *Store) SetSecret(ctx context.Context, key, value string) error {
	if value == "" {
		return s.DeleteSecret(ctx, key)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO secrets(key, value, updated_at) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Store) DeleteSecret(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE key=?`, key)
	return err
}

// AllSecrets — уся таблиця однією вибіркою; читачів двоє (auth і tunnel), і
// обом потрібно кілька ключів одразу.
func (s *Store) AllSecrets(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM secrets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// ResetAuth — стерти пароль, ключ сесій і токен HA. Тунель НЕ чіпається:
// забутий пароль не привід рвати зʼєднання, яке працює. Викликається
// командою `oddinvestd -reset-auth` (див. cmd/oddinvestd).
func (s *Store) ResetAuth(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE key IN (?,?,?)`,
		SecretPasswordHash, SecretSessionSecret, SecretHATokenHash)
	return err
}
