package internal

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

func maybeCreatePassword(usePassword bool) (string, string, error) {
	if !usePassword {
		return "", "", nil
	}

	password, err := generatePassword(8)
	if err != nil {
		return "", "", err
	}

	return password, hashSecret(password), nil
}

func checkPassword(meta Metadata, password string) error {
	if meta.PasswordHash == "" {
		return nil
	}

	if strings.TrimSpace(password) == "" || hashSecret(password) != meta.PasswordHash {
		return ErrInvalidPassword
	}

	return nil
}

func checkDeleteToken(meta Metadata, token string) error {
	if meta.DeleteTokenHash == "" || hashSecret(token) != meta.DeleteTokenHash {
		return ErrInvalidDeleteToken
	}

	return nil
}

func checkManageToken(meta Metadata, token string) error {
	if meta.ManageTokenHash == "" || hashSecret(token) != meta.ManageTokenHash {
		return ErrInvalidManageToken
	}

	return nil
}

func isExpired(meta Metadata, now time.Time) bool {
	if strings.EqualFold(meta.DataPolicy, "permanent") {
		return false
	}

	if meta.ExpiresAt.IsZero() {
		return false
	}

	return now.After(meta.ExpiresAt)
}

func validID(id string) bool {
	if len(id) < 1 || len(id) > 10 {
		return false
	}

	for _, r := range id {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '_' || r == '-' {
			continue
		}
		return false
	}

	return true
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func hashAdminPassword(password string, salt string) string {
	data := []byte(salt + ":" + password)

	sum := sha256.Sum256(data)
	for i := 0; i < 200000; i++ {
		next := sha256.Sum256(sum[:])
		sum = next
	}

	return base64.RawStdEncoding.EncodeToString(sum[:])
}

func secureCompare(candidate string, stored string) bool {
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(stored)) == 1
}

func generatePassword(length int) (string, error) {
	if length < 4 {
		length = 8
	}

	upper := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	lower := "abcdefghijklmnopqrstuvwxyz"
	digits := "0123456789"
	special := "!@#$%^&*_-+?{}[]"
	all := upper + lower + digits + special

	result := make([]byte, 0, length)

	a, err := randomChar(upper)
	if err != nil {
		return "", err
	}
	result = append(result, a)

	a, err = randomChar(lower)
	if err != nil {
		return "", err
	}
	result = append(result, a)

	a, err = randomChar(digits)
	if err != nil {
		return "", err
	}
	result = append(result, a)

	a, err = randomChar(special)
	if err != nil {
		return "", err
	}
	result = append(result, a)

	for len(result) < length {
		a, err = randomChar(all)
		if err != nil {
			return "", err
		}
		result = append(result, a)
	}

	if err := shuffleBytes(result); err != nil {
		return "", err
	}

	return string(result), nil
}

func randomChar(alphabet string) (byte, error) {
	n, err := randomIndex(len(alphabet))
	if err != nil {
		return 0, err
	}

	return alphabet[n], nil
}

func randomString(alphabet string, length int) (string, error) {
	result := make([]byte, length)

	for i := range result {
		ch, err := randomChar(alphabet)
		if err != nil {
			return "", err
		}
		result[i] = ch
	}

	return string(result), nil
}

func randomIndex(max int) (int, error) {
	if max <= 0 {
		return 0, errors.New("invalid max")
	}

	var b [1]byte

	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}

		limit := 256 - (256 % max)
		if int(b[0]) < limit {
			return int(b[0]) % max, nil
		}
	}
}

func shuffleBytes(data []byte) error {
	for i := len(data) - 1; i > 0; i-- {
		j, err := randomIndex(i + 1)
		if err != nil {
			return err
		}

		data[i], data[j] = data[j], data[i]
	}

	return nil
}

const idAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const tokenAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
