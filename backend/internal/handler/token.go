package handler

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func parseToken(tokenStr, secret string) (string, int64, error) {
	t, err := jwt.Parse(tokenStr, func(tk *jwt.Token) (interface{}, error) {
		if tk.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil || !t.Valid {
		return "", 0, errors.New("invalid token")
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return "", 0, errors.New("invalid claims")
	}
	sub, _ := claims["sub"].(string)
	version, _ := claims["ver"].(float64)
	if sub == "" || version < 1 || version != float64(int64(version)) {
		return "", 0, errors.New("empty subject")
	}
	return sub, int64(version), nil
}

var _ = time.Now
