package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

// GenerateJWT creates a signed HS256 JWT valid for 24 hours with subject = email.
func GenerateJWT(email string, secret string) (string, error) {
	if secret == "" {
		return "", errors.New("jwt secret is required")
	}

	now := time.Now().UTC()
	claims := Claims{
		Email: email,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   email,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ValidateJWT verifies the token string, ensuring it is signed with HS256 and unexpired.
// Returns the subject email if valid.
func ValidateJWT(tokenStr string, secret string) (string, error) {
	if secret == "" {
		return "", errors.New("jwt secret is required")
	}

	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})

	if err != nil {
		return "", err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return "", errors.New("invalid token")
	}

	if claims.Subject == "" {
		return "", errors.New("token subject is missing")
	}

	return claims.Subject, nil
}
