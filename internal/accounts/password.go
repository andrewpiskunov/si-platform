package accounts

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// A process-wide budget caps Argon2 memory at four concurrent 64 MiB jobs,
// including when multiple Services share a database. Waiting honors cancellation.
var passwordWorkers = make(chan struct{}, 4)

const passwordPrefix = "$argon2id$v=19$m=65536,t=3,p=2$"
const dummyPasswordHash = passwordPrefix + "AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func validPassword(password string) bool {
	return len(password) <= 128*4 && utf8.ValidString(password) && utf8.RuneCountInString(password) >= 12 && utf8.RuneCountInString(password) <= 128
}
func passwordKey(ctx context.Context, password string, salt []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case passwordWorkers <- struct{}{}:
		defer func() { <-passwordWorkers }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return key, nil
}
func hashPassword(ctx context.Context, password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", ErrUnavailable
	}
	key, err := passwordKey(ctx, password, salt)
	if err != nil {
		return "", err
	}
	return passwordPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func checkPassword(ctx context.Context, password, encoded string) (bool, error) {
	if !strings.HasPrefix(encoded, passwordPrefix) {
		return false, ErrUnavailable
	}
	parts := strings.Split(strings.TrimPrefix(encoded, passwordPrefix), "$")
	if len(parts) != 2 {
		return false, ErrUnavailable
	}
	salt, saltErr := base64.RawStdEncoding.DecodeString(parts[0])
	expected, keyErr := base64.RawStdEncoding.DecodeString(parts[1])
	if saltErr != nil || keyErr != nil || len(salt) != 16 || len(expected) != 32 {
		return false, ErrUnavailable
	}
	key, err := passwordKey(ctx, password, salt)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(key, expected) == 1, nil
}
