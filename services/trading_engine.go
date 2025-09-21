package services

import (
        "fmt"
        "log"
        "strconv"
        "strings"
        "sync"
        "time"
        "upbit-bitget-trading-bot/database"
        "upbit-bitget-trading-bot/models"
)

// safeGoTE starts a goroutine with panic recovery and restart-on-panic loop
func safeGoTE(name string, fn func()) {
        go func() {
                for {
                        func() {
                                defer func() {
                                        if r := recover(); r != nil {
                                                log.Printf("🚨 PANIC RECOVERED in TradingEngine.%s: %v", name, r)
                                                log.Printf("🔄 Restarting TradingEngine.%s in 10 seconds...", name)
                                                time.Sleep(10 * time.Second)
                                                return // This will restart the function
                                        }
                                }()
                                fn() // Execute function
                                
                                // If function exits normally, don't restart (expected for blocking functions)
                                log.Printf("ℹ️ TradingEngine.%s completed normally", name)
                                return
                        }()
                }
        }()
}

// TradingEngine coordinates between Upbit monitoring, Bitget trading, and Telegram notifications
type TradingEngine struct {
        upbitMonitor  *UpbitMonitor
        telegramBot   *TelegramBot
        encryptionKey string
        isRunning     bool
        stopChannel   chan bool
        
        // Concurrency controls to prevent crashes under multi-user load
        apiWorkerPool   chan struct{}           // Bounded worker pool for Bitget API calls (max 10 concurrent)
        userMutexes     map[int64]*sync.Mutex   // Per-user locks to prevent race conditions
        userMutexLock   sync.RWMutex           // Protects userMutexes map access
        updating        sync.Mutex             // Prevents overlapping position update cycles
}

// NewTradingEngine creates a new trading engine
func NewTradingEngine(upbitMonitor *UpbitMonitor, telegramBot *TelegramBot, encryptionKey string) *TradingEngine {
        return &TradingEngine{
                upbitMonitor:    upbitMonitor,
                telegramBot:     telegramBot,
                encryptionKey:   encryptionKey,
                isRunning:       false,
                stopChannel:     make(chan bool),
                apiWorkerPool:   make(chan struct{}, 10), // Max 10 concurrent API calls
                userMutexes:     make(map[int64]*sync.Mutex),
                userMutexLock:   sync.RWMutex{},
                updating:        sync.Mutex{},
        }
}

// Start starts the trading engine (blocking function)
func (te *TradingEngine) Start() {
        te.isRunning = true
        log.Println("🚀 Trading engine started")
        
        // Listen for new coins from Upbit monitor with panic recovery
        safeGoTE("processCoinDetections", te.processCoinDetections)
        
        // Start P&L monitoring for existing positions with panic recovery  
        safeGoTE("monitorPositions", te.monitorPositions)
        
        // Block here to keep the main TradingEngine alive
        // This prevents supervised restart from spawning duplicate goroutines
        select {
        case <-te.stopChannel:
                log.Println("🛑 TradingEngine received stop signal")
                te.isRunning = false
                return
        }
}

// Stop stops the trading engine
func (te *TradingEngine) Stop() {
        te.isRunning = false
        te.stopChannel <- true
        log.Println("🛑 Trading engine stopped")
}

// processCoinDetections handles new coin detections from Upbit
func (te *TradingEngine) processCoinDetections() {
        log.Println("👂 Listening for new coin detections...")
        
        for {
                select {
                case coinSymbol := <-te.upbitMonitor.GetNewCoinChannel():
                        log.Printf("🎯 Processing new coin: %s", coinSymbol)
                        te.handleNewCoin(coinSymbol)
                case testData := <-te.upbitMonitor.GetTestCoinChannel():
                        log.Printf("🧪 Processing test coin data: %s", testData)
                        te.handleTestCoin(testData)
                case <-te.stopChannel:
                        return
                }
        }
}

// handleNewCoin processes a newly detected coin with bounded concurrency
func (te *TradingEngine) handleNewCoin(coinSymbol string) {
        log.Printf("💰 Processing new coin detection: %s", coinSymbol)
        
        // Check database connectivity before trading
        if !database.IsConnected() {
                log.Printf("⚠️ Database not connected, skipping trading for coin %s", coinSymbol)
                return
        }
        
        // Get all active users
        var users []models.User
        err := database.WithDB(func(db *gorm.DB) error {
                return db.Where("is_active = ?", true).Find(&users).Error
        })
        if err != nil {
                if err.Error() == "database not available" {
                        log.Printf("⚠️ Database unavailable, skipping user processing")
                        return
                }
                log.Printf("❌ Failed to get active users: %v", err)
                return
        }
        
        if len(users) == 0 {
                log.Println("ℹ️ No active users found, skipping trading")
                return
        }
        
        log.Printf("👥 Found %d active users for trading", len(users))
        
        // Process trades for each active user with bounded concurrency
        for _, user := range users {
                // Capture loop variable to avoid closure issues
                userData := user
                coinData := coinSymbol
                safeGoTE("processUserTrade", func() {
                        // Acquire worker pool slot to prevent unbounded goroutines
                        te.apiWorkerPool <- struct{}{}
                        defer func() { <-te.apiWorkerPool }() // Release slot
                        
                        // Get per-user mutex to prevent race conditions
                        userMutex := te.getUserMutex(userData.TelegramID)
                        userMutex.Lock()
                        defer userMutex.Unlock()
                        
                        te.processUserTrade(userData, coinData)
                })
        }
}

// getUserMutex gets or creates a per-user mutex for synchronization
func (te *TradingEngine) getUserMutex(userID int64) *sync.Mutex {
        te.userMutexLock.RLock()
        if mutex, exists := te.userMutexes[userID]; exists {
                te.userMutexLock.RUnlock()
                return mutex
        }
        te.userMutexLock.RUnlock()
        
        // Need to create new mutex
        te.userMutexLock.Lock()
        defer te.userMutexLock.Unlock()
        
        // Double-check in case another goroutine created it
        if mutex, exists := te.userMutexes[userID]; exists {
                return mutex
        }
        
        mutex := &sync.Mutex{}
        te.userMutexes[userID] = mutex
        return mutex
}

// processUserTrade processes trading for a specific user
func (te *TradingEngine) processUserTrade(user models.User, coinSymbol string) {
        log.Printf("🔄 Processing trade for user %d, coin %s", user.TelegramID, coinSymbol)
        log.Printf("👤 User settings - TradeAmount: %.2f USDT, Leverage: %dx, TakeProfit: %.0f%%", 
                user.TradeAmount, user.Leverage, user.TakeProfitPercentage)
        
        // Get user's API credentials
        apiKey, apiSecret, passphrase, err := user.GetAPICredentials(te.encryptionKey)
        if err != nil {
                log.Printf("❌ Failed to get API credentials for user %d: %v", user.TelegramID, err)
                return
        }
        
        // Initialize Bitget API
        bitgetAPI := NewBitgetAPI(apiKey, apiSecret, passphrase)
        
        // Format symbol for Bitget (e.g., TOSHI -> TOSHIUSDT)
        symbol := bitgetAPI.FormatSymbol(coinSymbol)
        log.Printf("🪙 Formatted symbol: %s", symbol)
        
        // Check if symbol exists on Bitget
        if !bitgetAPI.IsSymbolValid(symbol) {
                log.Printf("⚠️ Symbol %s not available on Bitget for user %d", symbol, user.TelegramID)
                return
        }
        
        // Get current price
        currentPrice, err := bitgetAPI.GetSymbolPrice(symbol)
        if err != nil {
                log.Printf("❌ Failed to get price for %s: %v", symbol, err)
                return
        }
        
        log.Printf("📊 Current price for %s: $%.6f", symbol, currentPrice)
        
        // Calculate take profit price
        takeProfitPrice := currentPrice * (1 + user.TakeProfitPercentage/100)
        
        // Open long position using user's configured settings
        log.Printf("🚀 Opening long position for user %d: %s, amount: %.2f USDT, leverage: %dx", 
                user.TelegramID, symbol, user.TradeAmount, user.Leverage)
        
        orderResp, err := bitgetAPI.OpenLongPosition(symbol, user.TradeAmount, user.Leverage)
        if err != nil {
                log.Printf("❌ Failed to open position for user %d: %v", user.TelegramID, err)
                // Notify user about the error
                te.telegramBot.sendMessage(user.TelegramID, 
                        fmt.Sprintf("❌ %s pozisyonu açılamadı: %v", symbol, err))
                return
        }
        
        log.Printf("✅ Position opened successfully for user %d, order ID: %s", user.TelegramID, orderResp.OrderID)
        
        // Calculate position quantity based on margin and leverage
        marginUsed := user.TradeAmount
        quantity := (marginUsed * float64(user.Leverage)) / currentPrice
        
        // Save position to database
        position := &models.Position{
                PositionID:      orderResp.OrderID,
                UserID:          user.ID,
                CoinSymbol:      coinSymbol,
                Symbol:          symbol,
                EntryPrice:      currentPrice,
                CurrentPrice:    currentPrice,
                Quantity:        quantity,
                Leverage:        user.Leverage,
                TakeProfitPrice: takeProfitPrice,
                CurrentPNL:      0,
                ROE:             0,
                Status:          models.PositionOpen,
        }
        
        err = database.WithDB(func(db *gorm.DB) error {
                return db.Create(position).Error
        })
        if err != nil {
                if err.Error() == "database not available" {
                        log.Printf("⚠️ Database unavailable, position not saved (will be saved when DB reconnects)")
                } else {
                        log.Printf("❌ Failed to save position to database: %v", err)
                }
        } else {
                log.Printf("💾 Position saved to database with ID: %d", position.ID)
        }
        
        // Send notification to user
        te.telegramBot.SendTradeNotification(
                user.TelegramID,
                coinSymbol,
                orderResp.OrderID,
                currentPrice,
                takeProfitPrice,
                user.Leverage,
                user.TradeAmount,
        )
        
        log.Printf("📱 Trade notification sent to user %d", user.TelegramID)
}

// monitorPositions monitors existing positions for P&L updates and take profit
func (te *TradingEngine) monitorPositions() {
        log.Println("📊 Starting position monitoring...")
        
        ticker := time.NewTicker(3 * time.Minute) // Check every 3 minutes to reduce API load
        defer ticker.Stop()
        
        for {
                select {
                case <-ticker.C:
                        te.updateAllPositions()
                case <-te.stopChannel:
                        return
                }
        }
}

// updateAllPositions updates P&L for all open positions with bounded concurrency
func (te *TradingEngine) updateAllPositions() {
        // Check database connectivity first
        if !database.IsConnected() {
                log.Printf("⚠️ Database not connected, skipping position updates")
                return
        }
        
        // Prevent overlapping update cycles
        if !te.updating.TryLock() {
                log.Println("⏭️ Position update already in progress, skipping this cycle")
                return
        }
        defer te.updating.Unlock()
        
        // Get all open positions
        var positions []models.Position
        err := database.WithDB(func(db *gorm.DB) error {
                return db.Preload("User").Where("status = ?", models.PositionOpen).Find(&positions).Error
        })
        if err != nil {
                if err.Error() == "database not available" {
                        // Skip this cycle if DB became unavailable
                        return
                }
                log.Printf("❌ Failed to get open positions: %v", err)
                return
        }
        
        if len(positions) == 0 {
                return // No positions to update
        }
        
        log.Printf("📊 Updating %d open positions...", len(positions))
        
        for _, position := range positions {
                // Capture loop variable to avoid closure issues
                posData := position
                safeGoTE("updatePositionPNL", func() {
                        // Use worker pool to prevent unbounded goroutines
                        te.apiWorkerPool <- struct{}{}
                        defer func() { <-te.apiWorkerPool }()
                        
                        // Get per-user mutex for synchronization
                        userMutex := te.getUserMutex(posData.User.TelegramID)
                        userMutex.Lock()
                        defer userMutex.Unlock()
                        
                        te.updatePositionPNL(posData)
                })
        }
}

// updatePositionPNL updates P&L for a specific position
func (te *TradingEngine) updatePositionPNL(position models.Position) {
        // Get user's API credentials
        apiKey, apiSecret, passphrase, err := position.User.GetAPICredentials(te.encryptionKey)
        if err != nil {
                log.Printf("❌ Failed to get API credentials for position %d: %v", position.ID, err)
                return
        }
        
        // Initialize Bitget API
        bitgetAPI := NewBitgetAPI(apiKey, apiSecret, passphrase)
        
        // First check if position actually exists on Bitget
        bitgetPosition, err := bitgetAPI.GetPosition(position.Symbol)
        if err != nil || bitgetPosition == nil || bitgetPosition.Size == "0" {
                log.Printf("📊 Position %s no longer exists on Bitget, marking as closed in database", position.PositionID)
                
                // Position doesn't exist on Bitget anymore, mark as closed
                now := time.Now()
                position.Status = models.PositionClosed
                position.ClosedAt = &now
                
                err = database.WithDB(func(db *gorm.DB) error {
                        return db.Save(&position).Error
                })
                if err != nil {
                        if err.Error() == "database not available" {
                                log.Printf("⚠️ Database unavailable, position close not saved")
                        } else {
                                log.Printf("❌ Failed to close position %d in database: %v", position.ID, err)
                        }
                } else {
                        log.Printf("✅ Position %s automatically closed in database", position.PositionID)
                        
                        // Notify user that position was closed
                        te.telegramBot.sendMessage(position.User.TelegramID, 
                                fmt.Sprintf("ℹ️ Position %s was automatically closed (no longer exists on Bitget)", position.Symbol))
                }
                return
        }
        
        // Get current price
        currentPrice, err := bitgetAPI.GetSymbolPrice(position.Symbol)
        if err != nil {
                log.Printf("❌ Failed to get current price for %s: %v", position.Symbol, err)
                return
        }
        
        // Update position with current price and calculate P&L
        position.CurrentPrice = currentPrice
        position.CalculatePNL()
        
        // Save updated position
        err = database.WithDB(func(db *gorm.DB) error {
                return db.Save(&position).Error
        })
        if err != nil {
                if err.Error() == "database not available" {
                        log.Printf("⚠️ Database unavailable, position update not saved")
                } else {
                        log.Printf("❌ Failed to update position %d: %v", position.ID, err)
                }
                return
        }
        
        // Check if take profit should be executed
        if position.ShouldTakeProfit() {
                log.Printf("🎯 Take profit triggered for position %d (%s)", position.ID, position.Symbol)
                te.executeTakeProfit(position, bitgetAPI)
                return
        }
        
        // Send P&L update to user
        te.telegramBot.SendPNLUpdate(position.User.TelegramID, &position)
}

// executeTakeProfit executes take profit for a position
func (te *TradingEngine) executeTakeProfit(position models.Position, bitgetAPI *BitgetAPI) {
        log.Printf("💰 Executing take profit for position %d", position.ID)
        
        // Close the position
        _, err := bitgetAPI.ClosePosition(position.Symbol, position.Quantity, PositionSideLong)
        if err != nil {
                log.Printf("❌ Failed to close position %d: %v", position.ID, err)
                // Notify user about the error
                te.telegramBot.sendMessage(position.User.TelegramID,
                        fmt.Sprintf("❌ Take profit pozisyonu kapatılamadı: %v", err))
                return
        }
        
        // Update position status
        position.Status = models.PositionClosed
        closedAt := time.Now()
        position.ClosedAt = &closedAt
        
        err = database.WithDB(func(db *gorm.DB) error {
                return db.Save(&position).Error
        })
        if err != nil {
                if err.Error() == "database not available" {
                        log.Printf("⚠️ Database unavailable, take profit close not saved")
                } else {
                        log.Printf("❌ Failed to update closed position %d: %v", position.ID, err)
                }
        }
        
        // Notify user about successful take profit
        profitText := fmt.Sprintf(`🎯 *TAKE PROFIT EXECUTED*

💰 Coin: %s
📊 Entry: $%.6f | Exit: $%.6f
💵 P&L: $%.2f (%.2f%%)
🚀 ROE: %.2f%%
⏰ Pozisyon süresi: %s`,
                position.Symbol,
                position.EntryPrice,
                position.CurrentPrice,
                position.CurrentPNL,
                (position.CurrentPNL/position.EntryPrice)*100,
                position.ROE,
                time.Since(position.OpenedAt).String())
        
        te.telegramBot.sendMessage(position.User.TelegramID, profitText)
        
        log.Printf("✅ Take profit executed successfully for position %d", position.ID)
}

// handleTestCoin processes a test coin for a specific user only
func (te *TradingEngine) handleTestCoin(testData string) {
        // Parse testData: "coinSymbol:userID"
        parts := strings.Split(testData, ":")
        if len(parts) != 2 {
                log.Printf("❌ Invalid test data format: %s", testData)
                return
        }
        
        coinSymbol := parts[0]
        userIDStr := parts[1]
        
        userID, err := strconv.ParseInt(userIDStr, 10, 64)
        if err != nil {
                log.Printf("❌ Invalid user ID in test data: %s", userIDStr)
                return
        }
        
        log.Printf("🧪 Processing test coin %s for user %d ONLY", coinSymbol, userID)
        
        // Get specific user
        var user models.User
        err = database.WithDB(func(db *gorm.DB) error {
                return db.Where("telegram_id = ? AND is_active = ?", userID, true).First(&user).Error
        })
        if err != nil {
                if err.Error() == "database not available" {
                        log.Printf("⚠️ Database unavailable, test skipped for user %d", userID)
                } else {
                        log.Printf("❌ Failed to get user %d for test: %v", userID, err)
                }
                return
        }
        
        log.Printf("🧪 Test trade for user %d with coin %s", user.ID, coinSymbol)
        
        // Process trade for this user only - NO OTHER USERS
        te.processUserTrade(user, coinSymbol)
}
