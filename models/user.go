package models

import (
        "time"
)

type User struct {
        ID                    uint      `json:"id" gorm:"primaryKey"`
        TelegramID           int64     `json:"telegram_id" gorm:"uniqueIndex;not null"`
        Username             string    `json:"username" gorm:"size:100"`
        APIKey               string    `json:"-" gorm:"type:text"` // Encrypted API key
        APISecret            string    `json:"-" gorm:"type:text"` // Encrypted API secret
        Passphrase           string    `json:"-" gorm:"type:text"` // Encrypted passphrase
        TradeAmount          float64   `json:"trade_amount" gorm:"default:100"`        // USDT amount
        Leverage             int       `json:"leverage" gorm:"default:10"`             // 5x, 10x, 20x, 50x
        TakeProfitPercentage float64   `json:"take_profit_percentage" gorm:"default:200"` // 100%, 200%, 300%, 500%
        IsActive             bool      `json:"is_active" gorm:"default:false"`
        CreatedAt            time.Time `json:"created_at"`
        UpdatedAt            time.Time `json:"updated_at"`
        
        // Relations
        Positions []Position `json:"positions,omitempty"`
}

// SetAPICredentials encrypts and sets API credentials
func (u *User) SetAPICredentials(apiKey, apiSecret, passphrase, encryptionKey string) error {
        key, err := ParseEncryptionKey(encryptionKey)
        if err != nil {
                return err
        }
        
        u.APIKey, err = Encrypt(apiKey, key)
        if err != nil {
                return err
        }
        
        u.APISecret, err = Encrypt(apiSecret, key)
        if err != nil {
                return err
        }
        
        u.Passphrase, err = Encrypt(passphrase, key)
        if err != nil {
                return err
        }
        
        return nil
}

// GetAPICredentials decrypts and returns API credentials
func (u *User) GetAPICredentials(encryptionKey string) (apiKey, apiSecret, passphrase string, err error) {
        key, err := ParseEncryptionKey(encryptionKey)
        if err != nil {
                return "", "", "", err
        }
        
        apiKey, err = Decrypt(u.APIKey, key)
        if err != nil {
                return "", "", "", err
        }
        
        apiSecret, err = Decrypt(u.APISecret, key)
        if err != nil {
                return "", "", "", err
        }
        
        passphrase, err = Decrypt(u.Passphrase, key)
        if err != nil {
                return "", "", "", err
        }
        
        return apiKey, apiSecret, passphrase, nil
}

// UpdateAPICredentials encrypts and updates API credentials
func (u *User) UpdateAPICredentials(apiKey, apiSecret, passphrase, encryptionKey string) error {
        return u.SetAPICredentials(apiKey, apiSecret, passphrase, encryptionKey)
}
