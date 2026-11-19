package platform

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Users' documents are private, so they are encrypted before they reach the database (AES-256-GCM) and deleted
// after RETENTION_DAYS. Back up the key separately from the database: without it a backup is unreadable.

const sealPrefix = "enc:v1:"

var sealer cipher.AEAD

// LoadEncryptionKey reads DOCUMENT_ENCRYPTION_KEY: 32 random bytes in base64. Services call it at startup.
func LoadEncryptionKey() error {
	key, err := base64.StdEncoding.DecodeString(os.Getenv("DOCUMENT_ENCRYPTION_KEY"))
	if err != nil || len(key) != 32 {
		return errors.New("DOCUMENT_ENCRYPTION_KEY must be 32 bytes in base64")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	sealer, err = cipher.NewGCM(block)
	return err
}

func Seal(plain []byte) (string, error) {
	if sealer == nil {
		return "", errors.New("encryption key not loaded")
	}
	nonce := make([]byte, sealer.NonceSize())
	rand.Read(nonce)
	return sealPrefix + base64.StdEncoding.EncodeToString(sealer.Seal(nonce, nonce, plain, nil)), nil
}

// Open reverses Seal. Text without the prefix was stored before encryption existed and is returned as it is.
func Open(stored string) ([]byte, error) {
	encoded, sealed := strings.CutPrefix(stored, sealPrefix)
	if !sealed {
		return []byte(stored), nil
	}
	if sealer == nil {
		return nil, errors.New("encryption key not loaded")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) < sealer.NonceSize() {
		return nil, errors.New("stored data is damaged")
	}
	return sealer.Open(nil, data[:sealer.NonceSize()], data[sealer.NonceSize():], nil)
}

// SealJSON is Seal for jsonb columns: the result is a JSON string, so the column type stays the same.
func SealJSON(raw []byte) (string, error) {
	s, err := Seal(raw)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(s)
	return string(b), err
}

// OpenJSON reverses SealJSON. Anything but a JSON string is a row from before encryption.
func OpenJSON(raw []byte) ([]byte, error) {
	if len(raw) == 0 || raw[0] != '"' {
		return raw, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return Open(s)
}

// DeleteExpired removes rows of table older than days. table is a name from the code, never user input.
func DeleteExpired(ctx context.Context, pool *pgxpool.Pool, table string, days int) (int64, error) {
	tag, err := pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE created_at < now() - make_interval(days => $1)`,
		pgx.Identifier{table}.Sanitize()), days)
	return tag.RowsAffected(), err
}

// RunRetention deletes expired rows of table now and every hour, until ctx ends. It only returns early when
// RETENTION_DAYS is wrong, so a bad setting stops the service instead of silently keeping documents forever.
func RunRetention(ctx context.Context, pool *pgxpool.Pool, table string) error {
	days, err := strconv.Atoi(os.Getenv("RETENTION_DAYS"))
	if err != nil || days < 1 {
		return errors.New("RETENTION_DAYS must be a positive number")
	}
	for {
		if n, err := DeleteExpired(ctx, pool, table, days); err != nil {
			slog.Warn("retention failed, will retry", "table", table, "error", err)
		} else if n > 0 {
			slog.Info("expired rows deleted", "table", table, "rows", n)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Hour):
		}
	}
}
