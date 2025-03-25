package utils

import (
    "crypto/rand"
    "encoding/base64"
    "math/big"
)

func GenerateRandomString(length int) string {
    const charset = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
    result := make([]byte, length)
    for i := range result {
        n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
        result[i] = charset[n.Int64()]
    }
    return string(result)
}

func GenerateActivationCode() string {
    const charset = "0123456789"
    result := make([]byte, 8)
    for i := range result {
        n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
        result[i] = charset[n.Int64()]
    }
    return string(result)
}

func EncodeString(input string) string {
    return base64.StdEncoding.EncodeToString([]byte(input))
}

func DecodeString(encoded string) (string, error) {
    decoded, err := base64.StdEncoding.DecodeString(encoded)
    if err != nil {
        return "", err
    }
    return string(decoded), nil
}