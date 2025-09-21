package config

import (
        "log"
        "os"
        "strconv"

        "github.com/joho/godotenv"
)

type Config struct {
        DatabaseURL          string
        TelegramBotToken    string
        EncryptionKey       string
        UpbitCheckInterval  int  // seconds
        PNLUpdateInterval   int  // seconds
        Port               string
}

func Load() *Config {
        // Load .env file if it exists
        godotenv.Load()

        cfg := &Config{
                DatabaseURL:          getEnv("DATABASE_URL", ""),
                TelegramBotToken:     getEnv("TELEGRAM_BOT_TOKEN", ""),
                EncryptionKey:        getEnv("ENCRYPTION_KEY", ""),
                UpbitCheckInterval:   getEnvInt("UPBIT_CHECK_INTERVAL", 90), // Increased from 30s to 90s to prevent IP bans
                PNLUpdateInterval:    getEnvInt("PNL_UPDATE_INTERVAL", 60),
                Port:                getEnv("PORT", "5000"),
        }

        if cfg.DatabaseURL == "" {
                log.Fatal("DATABASE_URL environment variable is required")
        }
        
        if cfg.EncryptionKey == "" {
                log.Fatal("ENCRYPTION_KEY environment variable is required (32-byte base64 or hex encoded)")
        }

        return cfg
}

func getEnv(key, defaultValue string) string {
        if value := os.Getenv(key); value != "" {
                return value
        }
        return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
        if value := os.Getenv(key); value != "" {
                if intValue, err := strconv.Atoi(value); err == nil {
                        return intValue
                }
        }
        return defaultValue
}
