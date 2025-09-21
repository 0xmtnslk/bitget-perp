package models

import (
        "crypto/aes"
        "crypto/cipher"
        "crypto/rand"
        "encoding/base64"
        "encoding/hex"
        "errors"
        "io"
)

// Encrypt encrypts plaintext using AES-GCM with 32-byte key
func Encrypt(plaintext string, key []byte) (string, error) {
        if len(key) != 32 {
                return "", errors.New("encryption key must be exactly 32 bytes")
        }
        
        block, err := aes.NewCipher(key)
        if err != nil {
                return "", err
        }
        
        gcm, err := cipher.NewGCM(block)
        if err != nil {
                return "", err
        }
        
        nonce := make([]byte, gcm.NonceSize())
        if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
                return "", err
        }
        
        ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
        return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts ciphertext using AES-GCM with 32-byte key
func Decrypt(ciphertext string, key []byte) (string, error) {
        if len(key) != 32 {
                return "", errors.New("encryption key must be exactly 32 bytes")
        }
        
        data, err := base64.StdEncoding.DecodeString(ciphertext)
        if err != nil {
                return "", err
        }
        
        block, err := aes.NewCipher(key)
        if err != nil {
                return "", err
        }
        
        gcm, err := cipher.NewGCM(block)
        if err != nil {
                return "", err
        }
        
        nonceSize := gcm.NonceSize()
        if len(data) < nonceSize {
                return "", errors.New("ciphertext too short")
        }
        
        nonce, cipherData := data[:nonceSize], data[nonceSize:]
        plaintext, err := gcm.Open(nil, nonce, cipherData, nil)
        if err != nil {
                return "", err
        }
        
        return string(plaintext), nil
}

// ParseEncryptionKey parses base64 or hex encoded 32-byte key
func ParseEncryptionKey(keyStr string) ([]byte, error) {
        // Try base64 first
        if key, err := base64.StdEncoding.DecodeString(keyStr); err == nil && len(key) == 32 {
                return key, nil
        }
        
        // Try hex
        if key, err := hex.DecodeString(keyStr); err == nil && len(key) == 32 {
                return key, nil
        }
        
        // Direct bytes (for backward compatibility)
        if len([]byte(keyStr)) == 32 {
                return []byte(keyStr), nil
        }
        
        return nil, errors.New("encryption key must be 32 bytes encoded as base64 or hex")
}

// GenerateEncryptionKey generates a random 32-byte encryption key as base64
func GenerateEncryptionKey() (string, error) {
        key := make([]byte, 32)
        if _, err := rand.Read(key); err != nil {
                return "", err
        }
        return base64.StdEncoding.EncodeToString(key), nil
}
