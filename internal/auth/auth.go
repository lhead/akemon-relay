package auth

import (
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// ExtractBearer extracts the token from an "Authorization: Bearer xxx" header.
func ExtractBearer(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	return ""
}

// ExtractBearerFromString extracts token from a raw header value.
func ExtractBearerFromString(header string) string {
	if strings.HasPrefix(header, "Bearer ") {
		return header[7:]
	}
	return ""
}

// HashToken generates a bcrypt hash of a token.
func HashToken(token string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// VerifyToken checks a token against a bcrypt hash.
func VerifyToken(token, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(token)) == nil
}
