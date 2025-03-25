package handlers

import (
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "net/url"
    "strings"
)

const (
    HCAPTCHA_VERIFY_URL = "https://hcaptcha.com/siteverify"
    HCAPTCHA_SECRET = "ES_c55e4a2691f747069f498d54ab236d99"
)

type HCaptchaResponse struct {
    Success     bool     `json:"success"`
    ErrorCodes  []string `json:"error-codes"`
    ChallengeTS string  `json:"challenge_ts"`
    Hostname    string  `json:"hostname"`
}

func validateHCaptcha(token string) error {
    if token == "" {
        log.Println("Warning: No hCaptcha token provided")
        return nil
    }

    data := url.Values{}
    data.Set("secret", HCAPTCHA_SECRET)
    data.Set("response", token)

    client := &http.Client{}
    resp, err := client.PostForm(HCAPTCHA_VERIFY_URL, data)
    if err != nil {
        return fmt.Errorf("failed to contact hCaptcha server: %v", err)
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return fmt.Errorf("failed to read hCaptcha response: %v", err)
    }

    var result HCaptchaResponse
    if err := json.Unmarshal(body, &result); err != nil {
        return fmt.Errorf("failed to parse hCaptcha response: %v", err)
    }

    if !result.Success {
        errorMsg := "hCaptcha validation failed"
        if len(result.ErrorCodes) > 0 {
            if result.ErrorCodes[0] == "already-seen-response" {
                log.Printf("Warning: hCaptcha token reuse attempted: %v", result.ErrorCodes)
                return nil 
            }
            errorMsg = fmt.Sprintf("%s: %s", errorMsg, strings.Join(result.ErrorCodes, ", "))
        }
        return fmt.Errorf(errorMsg)
    }

    return nil
}