package database

import (
        "fmt"
        "log"
        "sync/atomic"
        "time"
        "upbit-bitget-trading-bot/models"

        "gorm.io/driver/postgres"
        "gorm.io/gorm"
        "gorm.io/gorm/logger"
)

var DB *gorm.DB
var isConnected int64 // Atomic boolean for connection status
var databaseURL string // Store for auto-reconnection

// IsConnected returns true if database is connected and healthy
func IsConnected() bool {
        return atomic.LoadInt64(&isConnected) == 1
}

// setConnected updates the connection status
func setConnected(connected bool) {
        if connected {
                atomic.StoreInt64(&isConnected, 1)
        } else {
                atomic.StoreInt64(&isConnected, 0)
        }
}

// Connect establishes database connection and runs migrations
func Connect(dbURL string) error {
        databaseURL = dbURL // Store for auto-reconnection
        var err error
        
        // Configure GORM with secure logger (Warn level to prevent sensitive data leaks)
        config := &gorm.Config{
                Logger: logger.Default.LogMode(logger.Warn),
        }
        
        // Connect to PostgreSQL
        DB, err = gorm.Open(postgres.Open(dbURL), config)
        if err != nil {
                return fmt.Errorf("failed to connect to database: %w", err)
        }
        
        log.Println("🔗 Database connected successfully!")
        
        // Run auto migrations
        if err := AutoMigrate(); err != nil {
                setConnected(false)
                return fmt.Errorf("failed to run migrations: %w", err)
        }
        
        setConnected(true) // Mark as connected after successful migration
        
        // Start background health monitoring
        go startHealthMonitoring()
        
        return nil
}

// AutoMigrate runs database migrations
func AutoMigrate() error {
        log.Println("🔄 Running database migrations...")
        
        err := DB.AutoMigrate(
                &models.User{},
                &models.Position{},
        )
        
        if err != nil {
                return err
        }
        
        log.Println("✅ Database migrations completed!")
        return nil
}

// Close closes the database connection
func Close() error {
        if DB != nil {
                sqlDB, err := DB.DB()
                if err != nil {
                        return err
                }
                DB = nil
                setConnected(false) // Mark as disconnected
                return sqlDB.Close()
        }
        return nil
}

// StartReconnectionSupervisor starts the database reconnection supervisor
// This runs independently of initial connection success
func StartReconnectionSupervisor() {
        log.Printf("🔄 Database reconnection supervisor starting...")
        startHealthMonitoring()
}

// startHealthMonitoring starts background DB health monitoring with auto-reconnection
func startHealthMonitoring() {
        for {
                time.Sleep(30 * time.Second) // Check every 30 seconds
                
                if DB == nil || !IsConnected() {
                        log.Printf("⚠️ Database disconnected, attempting reconnection...")
                        if err := attemptReconnection(); err != nil {
                                log.Printf("❌ Reconnection failed: %v", err)
                                setConnected(false)
                        }
                        continue
                }
                
                // Ping database to check health
                sqlDB, err := DB.DB()
                if err != nil {
                        log.Printf("⚠️ Failed to get SQL DB instance: %v", err)
                        setConnected(false)
                        continue
                }
                
                if err := sqlDB.Ping(); err != nil {
                        log.Printf("⚠️ Database ping failed: %v", err)
                        setConnected(false)
                        // Connection lost - next cycle will try to reconnect
                } else {
                        // Ping successful - ensure we're marked as connected
                        if !IsConnected() {
                                log.Printf("✅ Database reconnected successfully!")
                                setConnected(true)
                        }
                }
        }
}

// attemptReconnection attempts to reconnect to the database
func attemptReconnection() error {
        if databaseURL == "" {
                return fmt.Errorf("no database URL stored for reconnection")
        }
        
        // Configure GORM with secure logger
        config := &gorm.Config{
                Logger: logger.Default.LogMode(logger.Warn),
        }
        
        // Attempt to reconnect
        newDB, err := gorm.Open(postgres.Open(databaseURL), config)
        if err != nil {
                return fmt.Errorf("failed to reconnect to database: %w", err)
        }
        
        // Test the connection with ping
        sqlDB, err := newDB.DB()
        if err != nil {
                return fmt.Errorf("failed to get SQL DB instance: %w", err)
        }
        
        if err := sqlDB.Ping(); err != nil {
                return fmt.Errorf("failed to ping database after reconnection: %w", err)
        }
        
        // Successful reconnection
        DB = newDB
        setConnected(true)
        log.Printf("✅ Database auto-reconnection successful!")
        
        return nil
}

// GetDB returns the database instance
func GetDB() *gorm.DB {
        return DB
}

// WithDB executes a function with database connection if available
// Returns error if database is not connected
func WithDB(fn func(*gorm.DB) error) error {
        if !IsConnected() || DB == nil {
                return fmt.Errorf("database not available")
        }
        return fn(DB)
}

// GetIfConnected returns DB only if connected, nil otherwise  
func GetIfConnected() *gorm.DB {
        if IsConnected() && DB != nil {
                return DB
        }
        return nil
}
