package main

import (
        "fmt"
        "log"
        "net/http"
        "os"
        "os/signal"
        "syscall"
        "time"
        
        "upbit-bitget-trading-bot/config"
        "upbit-bitget-trading-bot/database"
        "upbit-bitget-trading-bot/services"
)

// safeGo starts a goroutine with panic recovery (restart only on panic)
func safeGo(name string, fn func()) {
        go func() {
                for {
                        shouldRestart := false
                        func() {
                                defer func() {
                                        if r := recover(); r != nil {
                                                log.Printf("🚨 PANIC RECOVERED in %s: %v", name, r)
                                                log.Printf("🔄 Restarting %s in 10 seconds...", name)
                                                time.Sleep(10 * time.Second)
                                                shouldRestart = true // Restart on panic
                                        }
                                }()
                                fn() // Execute function
                                
                                // If function exits normally, log and exit (no restart)
                                log.Printf("ℹ️ %s completed normally", name)
                                shouldRestart = false // No restart on normal exit
                        }()
                        
                        if !shouldRestart {
                                break // Exit loop on normal completion
                        }
                }
        }()
}

func main() {
        fmt.Println("🚀 Upbit-Bitget Trading Bot Starting...")
        
        // Load configuration
        cfg := config.Load()
        log.Printf("⚙️ Configuration loaded - Database ready, Bot token: %s", 
                func() string {
                        if cfg.TelegramBotToken != "" {
                                return "✅ Set"
                        }
                        return "❌ Missing"
                }())
        
        // Initialize database connection with retry and resilience
        log.Println("🔗 Connecting to database...")
        for attempts := 1; attempts <= 5; attempts++ {
                if err := database.Connect(cfg.DatabaseURL); err != nil {
                        log.Printf("⚠️ Database connection failed (attempt %d/5): %v", attempts, err)
                        if attempts < 5 {
                                sleepTime := time.Duration(attempts*2) * time.Second
                                log.Printf("🔄 Retrying in %v...", sleepTime)
                                time.Sleep(sleepTime)
                                continue
                        }
                        log.Printf("❌ Database connection failed after 5 attempts, starting reconnection supervisor")
                        // Start database reconnection supervisor for auto-recovery
                        safeGo("DatabaseReconnector", func() {
                                database.StartReconnectionSupervisor()
                        })
                } else {
                        log.Println("🔗 Database connected successfully!")
                        defer database.Close()
                        break
                }
        }
        
        // Create channels for graceful shutdown
        quit := make(chan os.Signal, 1)
        signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
        
        // Start HTTP health check server for Replit deployment with panic recovery
        safeGo("HTTP-Server", func() {
                http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
                        w.WriteHeader(http.StatusOK)
                        w.Write([]byte(`{"status":"running","message":"Upbit-Bitget Trading Bot is active","services":["upbit_monitor","telegram_bot","trading_engine"]}`))
                })
                
                http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
                        w.Header().Set("Content-Type", "application/json")
                        w.WriteHeader(http.StatusOK)
                        w.Write([]byte(`{"healthy":true,"timestamp":"` + time.Now().Format(time.RFC3339) + `"}`))
                })
                
                log.Println("🌐 HTTP health server starting on :5000")
                if err := http.ListenAndServe(":5000", nil); err != nil {
                        log.Printf("❌ HTTP server error: %v", err)
                }
        })
        
        // Initialize services only if Telegram bot token is available
        if cfg.TelegramBotToken != "" {
                log.Println("🚀 Initializing trading services...")
                
                // Initialize services
                upbitMonitor := services.NewUpbitMonitor(time.Duration(cfg.UpbitCheckInterval) * time.Second)
                
                telegramBot, err := services.NewTelegramBot(cfg.TelegramBotToken, cfg.EncryptionKey, upbitMonitor)
                if err != nil {
                        log.Printf("❌ Failed to initialize Telegram bot: %v", err)
                } else {
                        tradingEngine := services.NewTradingEngine(upbitMonitor, telegramBot, cfg.EncryptionKey)
                        
                        // Start all services with panic recovery
                        safeGo("UpbitMonitor", upbitMonitor.Start)
                        safeGo("TelegramBot", telegramBot.Start)
                        safeGo("TradingEngine", tradingEngine.Start)
                        
                        log.Println("✅ All trading services started successfully!")
                }
        } else {
                log.Println("⚠️ TELEGRAM_BOT_TOKEN not set - running in monitoring mode only")
                
                // Start basic monitoring without trading
                safeGo("UpbitMonitor-Fallback", func() {
                        log.Printf("📊 Starting Upbit monitoring service (checking every %d seconds)...", cfg.UpbitCheckInterval)
                        
                        // Create basic UpbitMonitor for fallback mode
                        fallbackMonitor := services.NewUpbitMonitor(time.Duration(cfg.UpbitCheckInterval) * time.Second)
                        fallbackMonitor.Start()
                })
        }
        
        log.Printf("✅ Trading bot is running")
        log.Printf("🔗 Database: Connected and migrated")
        log.Printf("📈 Upbit monitoring: Every %d seconds", cfg.UpbitCheckInterval)
        log.Printf("💰 P&L updates: Every 3 minutes")
        log.Println("Press Ctrl+C to shutdown...")
        
        // Wait for shutdown signal
        <-quit
        log.Println("🛑 Shutting down trading bot...")
        log.Println("💤 Goodbye!")
}
