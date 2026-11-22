package turnstile

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Verifier interface {
	Verify(ctx context.Context, token, remoteIP string) (bool, error)
}

type TurnstileVerifier struct {
	SecretKey string
	client    *http.Client
}

func NewVerifier(secretKey string) *TurnstileVerifier {
	return &TurnstileVerifier{
		SecretKey: strings.TrimSpace(secretKey),
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

type siteVerifyResponse struct {
	Success     bool     `json:"success"`
	ErrorCodes  []string `json:"error-codes"`
	ChallengeTS string   `json:"challenge_ts"`
	Hostname    string   `json:"hostname"`
}

func (v *TurnstileVerifier) Verify(ctx context.Context, token, remoteIP string) (bool, error) {
	if v.SecretKey == "" {
		log.Println("[WARN] TURNSTILE_SECRET_KEY is empty; skipping Turnstile verification in local dev mode")
		return true, nil
	}

	if token == "" {
		return false, fmt.Errorf("missing turnstile token")
	}

	form := url.Values{}
	form.Set("secret", v.SecretKey)
	form.Set("response", token)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://challenges.cloudflare.com/turnstile/v0/siteverify", strings.NewReader(form.Encode()))
	if err != nil {
		return false, fmt.Errorf("create turnstile request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("do turnstile verify request: %w", err)
	}
	defer resp.Body.Close()

	var result siteVerifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("decode turnstile response: %w", err)
	}

	return result.Success, nil
}
