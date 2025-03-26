package database

import (
    "math/rand"
)

func generateRandomCode(length int) string {
    const charset = "0123456789"
    code := make([]byte, length)
    for i := range code {
        code[i] = charset[rand.Intn(len(charset))]
    }
    return string(code)
}

