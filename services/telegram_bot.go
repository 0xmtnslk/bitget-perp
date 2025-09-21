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

        tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TelegramBot handles Telegram bot operations
type TelegramBot struct {
        Bot           *tgbotapi.BotAPI
        EncryptionKey string
        UpdateChannel tgbotapi.UpdatesChannel
        upbitMonitor  *UpbitMonitor // For testing purposes
        
        // Per-user rate limiting to prevent API overload
        userRateLimits map[int64]*time.Ticker
        rateLimitMutex sync.RWMutex
}

// UserState represents the current state of user interaction
type UserState struct {
        State string
        Data  map[string]interface{}
}

var userStates = make(map[int64]*UserState)

// Helper function for min
func min(a, b int) int {
        if a < b {
                return a
        }
        return b
}

// NewTelegramBot creates a new Telegram bot instance
func NewTelegramBot(token, encryptionKey string, upbitMonitor *UpbitMonitor) (*TelegramBot, error) {
        bot, err := tgbotapi.NewBotAPI(token)
        if err != nil {
                return nil, fmt.Errorf("failed to create bot: %w", err)
        }
        
        bot.Debug = false
        log.Printf("🤖 Telegram bot authorized: @%s", bot.Self.UserName)
        
        // Set up updates
        u := tgbotapi.NewUpdate(0)
        u.Timeout = 60
        
        updates := bot.GetUpdatesChan(u)
        
        return &TelegramBot{
                Bot:            bot,
                EncryptionKey:  encryptionKey,
                UpdateChannel:  updates,
                upbitMonitor:   upbitMonitor,
                userRateLimits: make(map[int64]*time.Ticker),
                rateLimitMutex: sync.RWMutex{},
        }, nil
}

// Start starts the Telegram bot with supervised restart (no recursion)
func (tb *TelegramBot) Start() {
        log.Println("🚀 Starting Telegram bot...")
        
        for {
                // Supervised restart loop - no recursion risk
                func() {
                        defer func() {
                                if r := recover(); r != nil {
                                        log.Printf("🚨 PANIC RECOVERED in TelegramBot update loop: %v", r)
                                        log.Printf("🔄 Recreating Telegram connection in 5 seconds...")
                                        time.Sleep(5 * time.Second)
                                }
                        }()
                        
                        // Ensure we have a valid UpdateChannel
                        if tb.UpdateChannel == nil {
                                log.Printf("⚠️ UpdateChannel is nil, recreating...")
                                tb.recreateUpdateChannel()
                        }
                        
                        // Process updates until channel closes or panic
                        for update := range tb.UpdateChannel {
                                tb.handleUpdateSafely(update)
                        }
                        
                        // Channel closed - recreate connection
                        log.Printf("⚠️ Telegram UpdateChannel closed, recreating connection...")
                        tb.recreateUpdateChannel()
                }()
                
                // Brief pause before retry to avoid tight loop
                time.Sleep(2 * time.Second)
        }
}

// handleUpdateSafely handles individual updates with panic recovery
func (tb *TelegramBot) handleUpdateSafely(update tgbotapi.Update) {
        defer func() {
                if r := recover(); r != nil {
                        log.Printf("🚨 PANIC RECOVERED in Telegram update handler: %v", r)
                        // Continue processing other messages
                }
        }()
        
        if update.Message != nil {
                tb.handleMessage(update.Message)
        } else if update.CallbackQuery != nil {
                tb.handleCallbackQuery(update.CallbackQuery)
        }
}

// recreateUpdateChannel recreates the update channel after disconnection
func (tb *TelegramBot) recreateUpdateChannel() {
        defer func() {
                if r := recover(); r != nil {
                        log.Printf("🚨 PANIC RECOVERED in recreateUpdateChannel: %v", r)
                }
        }()
        
        // Stop existing updates if any
        if tb.Bot != nil {
                tb.Bot.StopReceivingUpdates()
        }
        
        // Recreate BotAPI client in case of auth issues
        bot, err := tgbotapi.NewBotAPI(tb.Bot.Token)
        if err != nil {
                log.Printf("❌ Failed to recreate Telegram bot: %v", err)
                time.Sleep(10 * time.Second)
                return
        }
        
        bot.Debug = false
        tb.Bot = bot
        
        // Create new update channel
        u := tgbotapi.NewUpdate(0)
        u.Timeout = 60
        
        tb.UpdateChannel = tb.Bot.GetUpdatesChan(u)
        log.Printf("✅ Telegram UpdateChannel recreated successfully")
}

// Stop stops the Telegram bot
func (tb *TelegramBot) Stop() {
        tb.Bot.StopReceivingUpdates()
}

// handleMessage handles incoming messages with database-aware protection
func (tb *TelegramBot) handleMessage(message *tgbotapi.Message) {
        userID := message.From.ID
        chatID := message.Chat.ID
        text := message.Text
        
        log.Printf("📨 Message from %s (@%s): %s", message.From.FirstName, message.From.UserName, text)
        
        // Get or create user state
        state := tb.getUserState(userID)
        
        switch {
        case text == "/start" || text == "🏠 Ana Sayfa":
                tb.handleStartCommand(chatID, userID, message.From)
        case text == "/register" || text == "📝 Kayıt Ol":
                if !database.IsConnected() {
                        tb.sendMessage(chatID, "⚠️ Database is currently unavailable. Please try again later.")
                        return
                }
                tb.handleRegisterCommand(chatID, userID)
        case text == "/settings" || text == "⚙️ Ayarlar":
                if !database.IsConnected() {
                        tb.sendMessage(chatID, "⚠️ Database is currently unavailable. Cannot access settings.")
                        return
                }
                tb.handleSettingsCommand(chatID, userID)
        case text == "/update_api" || text == "🔑 API Güncelle":
                tb.handleUpdateAPICommand(chatID, userID)
        case text == "/status" || text == "📊 Pozisyonlar":
                if !database.IsConnected() {
                        tb.sendMessage(chatID, "⚠️ Database is currently unavailable. Cannot retrieve positions.")
                        return
                }
                tb.handleStatusCommand(chatID, userID)
        case text == "/balance" || text == "💰 Bakiye":
                tb.handleBalanceCommand(chatID, userID)
        case text == "/test" || text == "🧪 Test":
                tb.handleTestCommand(chatID, userID)
        case text == "/help" || text == "❓ Yardım":
                tb.handleHelpCommand(chatID)
        case state.State == "awaiting_api_key":
                tb.handleAPIKeyInput(chatID, userID, text)
        case state.State == "awaiting_api_secret":
                tb.handleAPISecretInput(chatID, userID, text)
        case state.State == "awaiting_passphrase":
                tb.handlePassphraseInput(chatID, userID, text)
        case state.State == "awaiting_update_api_key":
                tb.handleUpdateAPIKeyInput(chatID, userID, text)
        case state.State == "awaiting_update_api_secret":
                tb.handleUpdateAPISecretInput(chatID, userID, text)
        case state.State == "awaiting_update_passphrase":
                tb.handleUpdatePassphraseInput(chatID, userID, text)
        case state.State == "awaiting_trade_amount":
                tb.handleTradeAmountInput(chatID, userID, text)
        case state.State == "awaiting_leverage":
                tb.handleLeverageInput(chatID, userID, text)
        case state.State == "awaiting_take_profit":
                tb.handleTakeProfitInput(chatID, userID, text)
        default:
                tb.sendMessageWithMenu(chatID, "❓ Bilinmeyen komut. Menüden istediğiniz komutu seçin:")
        }
}

// handleCallbackQuery handles callback queries from inline keyboards
func (tb *TelegramBot) handleCallbackQuery(callbackQuery *tgbotapi.CallbackQuery) {
        chatID := callbackQuery.Message.Chat.ID
        userID := callbackQuery.From.ID
        data := callbackQuery.Data
        
        log.Printf("🔘 Callback from %s: %s", callbackQuery.From.FirstName, data)
        
        // Acknowledge the callback
        callback := tgbotapi.NewCallback(callbackQuery.ID, "")
        tb.Bot.Request(callback)
        
        switch {
        case strings.HasPrefix(data, "close_position_"):
                positionID := strings.TrimPrefix(data, "close_position_")
                tb.handleClosePositionCallback(chatID, userID, positionID)
        case data == "confirm_close":
                tb.handleConfirmCloseCallback(chatID, userID)
        case data == "cancel_close":
                tb.handleCancelCloseCallback(chatID)
        case data == "set_trade_amount":
                tb.handleTradeAmountCallback(chatID, userID, "")
        case data == "set_leverage":
                tb.handleLeverageCallback(chatID, userID, "")
        case data == "set_take_profit":
                tb.handleTakeProfitCallback(chatID, userID, "")
        case strings.HasPrefix(data, "amount_"):
                amount := strings.TrimPrefix(data, "amount_")
                tb.handleAmountSelectionCallback(chatID, userID, amount)
        case strings.HasPrefix(data, "leverage_"):
                leverage := strings.TrimPrefix(data, "leverage_")
                tb.handleLeverageSelectionCallback(chatID, userID, leverage)
        case strings.HasPrefix(data, "tp_"):
                takeProfit := strings.TrimPrefix(data, "tp_")
                tb.handleTakeProfitSelectionCallback(chatID, userID, takeProfit)
        case strings.HasPrefix(data, "test_"):
                coinSymbol := strings.TrimPrefix(data, "test_")
                tb.handleTestCoinCallback(chatID, userID, coinSymbol)
        case strings.HasPrefix(data, "confirm_test_"):
                coinSymbol := strings.TrimPrefix(data, "confirm_test_")
                tb.handleConfirmTestCallback(chatID, userID, coinSymbol)
        case data == "cancel_test":
                tb.sendMessage(chatID, "❌ Test iptali edildi.")
        case data == "toggle_active":
                tb.handleToggleActiveCallback(chatID, userID)
        }
}

// handleStartCommand handles /start command
func (tb *TelegramBot) handleStartCommand(chatID int64, userID int64, from *tgbotapi.User) {
        welcomeText := fmt.Sprintf(`🚀 *Upbit-Bitget Trading Bot'una Hoşgeldiniz!*

Merhaba %s! 👋

Bu bot Upbit'te yeni listelenen coinleri otomatik tespit edip, Bitget futures borsasında long pozisyon açar.

🔧 *Başlamak için:*
1. 📝 Kayıt ol - API anahtarlarınızı girin
2. ⚙️ Ayarlar - Trading ayarlarınızı yapın
3. Bot otomatik olarak çalışmaya başlar!

⚠️ *Önemli:* Bu bot gerçek para ile işlem yapar. Lütfen dikkatli kullanın!

👇 *Alttaki menüden istediğiniz komutu seçin:*`, from.FirstName)
        
        tb.sendMessageWithMenu(chatID, welcomeText)
}

// handleRegisterCommand handles /register command
func (tb *TelegramBot) handleRegisterCommand(chatID int64, userID int64) {
        // Check if user already exists
        user, err := tb.getUser(userID)
        if err == nil && user != nil {
                tb.sendMessage(chatID, "✅ Zaten kayıtlısınız! /settings ile ayarlarınızı güncelleyebilirsiniz.")
                return
        }
        
        // Create new user
        user = &models.User{
                TelegramID:           userID,
                TradeAmount:         100,
                Leverage:            10,
                TakeProfitPercentage: 200,
                IsActive:            false,
        }
        
        if err := database.DB.Create(user).Error; err != nil {
                log.Printf("❌ Failed to create user: %v", err)
                tb.sendMessage(chatID, "❌ Kayıt sırasında hata oluştu. Lütfen tekrar deneyin.")
                return
        }
        
        text := `🔐 *API Anahtarlarınızı Girin*

Bitget futures hesabınızın API anahtarlarını girmeniz gerekiyor:

📝 *Bitget API Key'inizi girin:*
(API anahtarınız güvenli şekilde şifrelenerek saklanacak)`
        
        tb.sendMessage(chatID, text)
        tb.setUserState(userID, "awaiting_api_key", nil)
}

// handleAPIKeyInput handles API key input
func (tb *TelegramBot) handleAPIKeyInput(chatID int64, userID int64, apiKey string) {
        state := tb.getUserState(userID)
        state.Data = map[string]interface{}{"api_key": apiKey}
        
        tb.sendMessage(chatID, "🔑 *API Secret'inizi girin:*")
        tb.setUserState(userID, "awaiting_api_secret", state.Data)
}

// handleAPISecretInput handles API secret input
func (tb *TelegramBot) handleAPISecretInput(chatID int64, userID int64, apiSecret string) {
        state := tb.getUserState(userID)
        state.Data["api_secret"] = apiSecret
        
        tb.sendMessage(chatID, "🔐 *Passphrase'inizi girin:*")
        tb.setUserState(userID, "awaiting_passphrase", state.Data)
}

// handlePassphraseInput handles passphrase input and saves credentials
func (tb *TelegramBot) handlePassphraseInput(chatID int64, userID int64, passphrase string) {
        state := tb.getUserState(userID)
        apiKey := state.Data["api_key"].(string)
        apiSecret := state.Data["api_secret"].(string)
        
        // Get user and save credentials
        user, err := tb.getUser(userID)
        if err != nil {
                tb.sendMessage(chatID, "❌ Kullanıcı bulunamadı. Lütfen /register ile tekrar kayıt olun.")
                tb.clearUserState(userID)
                return
        }
        
        if err := user.SetAPICredentials(apiKey, apiSecret, passphrase, tb.EncryptionKey); err != nil {
                log.Printf("❌ Failed to encrypt credentials: %v", err)
                tb.sendMessage(chatID, "❌ API anahtarları kaydedilirken hata oluştu.")
                tb.clearUserState(userID)
                return
        }
        
        if err := database.DB.Save(user).Error; err != nil {
                log.Printf("❌ Failed to save user: %v", err)
                tb.sendMessage(chatID, "❌ Bilgiler kaydedilirken hata oluştu.")
                tb.clearUserState(userID)
                return
        }
        
        text := `✅ *API anahtarları başarıyla kaydedildi!*

🔧 *Şimdi trading ayarlarınızı yapın:*
/settings - Ayarları düzenle

⚙️ *Varsayılan Ayarlar:*
• Trade Amount: 100 USDT
• Leverage: 10x  
• Take Profit: %200

Bot şu anda pasif durumda. Ayarlarınızı tamamladıktan sonra aktif hale getirebilirsiniz.`
        
        tb.sendMessage(chatID, text)
        tb.clearUserState(userID)
}

// handleSettingsCommand handles /settings command
func (tb *TelegramBot) handleSettingsCommand(chatID int64, userID int64) {
        user, err := tb.getUser(userID)
        if err != nil {
                tb.sendMessage(chatID, "❌ Önce /register ile kayıt olmanız gerekiyor.")
                return
        }
        
        statusEmoji := "❌"
        statusText := "Pasif"
        if user.IsActive {
                statusEmoji = "✅"
                statusText = "Aktif"
        }
        
        text := fmt.Sprintf(`⚙️ *Trading Ayarlarınız*

💰 Trade Amount: %.0f USDT
🔧 Leverage: %dx
📈 Take Profit: %.0f%%
%s Status: %s

🔧 *Ayarları Değiştir:*`, 
                user.TradeAmount, user.Leverage, user.TakeProfitPercentage, statusEmoji, statusText)
        
        keyboard := tgbotapi.NewInlineKeyboardMarkup(
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("💰 Trade Amount", "set_trade_amount"),
                        tgbotapi.NewInlineKeyboardButtonData("🔧 Leverage", "set_leverage"),
                ),
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("📈 Take Profit", "set_take_profit"),
                        tgbotapi.NewInlineKeyboardButtonData("🔄 Aktif/Pasif", "toggle_active"),
                ),
        )
        
        msg := tgbotapi.NewMessage(chatID, text)
        msg.ReplyMarkup = keyboard
        msg.ParseMode = "Markdown"
        tb.Bot.Send(msg)
}

// SendTradeNotification sends trading notification to user
func (tb *TelegramBot) SendTradeNotification(userID int64, coin, positionID string, entryPrice, takeProfitPrice float64, leverage int, amount float64) {
        text := fmt.Sprintf(`🚀 *YENİ POZİSYON AÇILDI*

💰 Coin: %s/USDT
💵 Miktar: %.0f USDT
🔧 Leverage: %dx
📊 Entry Price: $%.6f
🎯 Take Profit: $%.6f (%.0f%%)
🆔 Pozisyon ID: #%s
⏰ %s`, 
                coin, amount, leverage, entryPrice, takeProfitPrice, 
                ((takeProfitPrice/entryPrice)-1)*100, positionID, 
                fmt.Sprintf("%s", "şimdi"))
        
        // Add emergency close button
        keyboard := tgbotapi.NewInlineKeyboardMarkup(
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("🔴 ACİL KAPAT", "close_position_"+positionID),
                ),
        )
        
        msg := tgbotapi.NewMessage(userID, text)
        msg.ReplyMarkup = keyboard
        msg.ParseMode = "Markdown"
        tb.Bot.Send(msg)
}

// SendPNLUpdate sends P&L update to user
func (tb *TelegramBot) SendPNLUpdate(userID int64, position *models.Position) {
        pnlEmoji := "📉"
        if position.CurrentPNL > 0 {
                pnlEmoji = "📈"
        }
        
        text := fmt.Sprintf(`📊 *POZİSYON DURUMU*

💰 Coin: %s
📊 Entry: $%.6f | Current: $%.6f
%s P&L: $%.2f (%.2f%%)
🚀 ROE: %.2f%%
⏰ %s`,
                position.Symbol, position.EntryPrice, position.CurrentPrice,
                pnlEmoji, position.CurrentPNL, (position.CurrentPNL/position.EntryPrice)*100,
                position.ROE, "1 dakika önce")
        
        // Add emergency close button
        keyboard := tgbotapi.NewInlineKeyboardMarkup(
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("🔴 ACİL KAPAT", "close_position_"+position.PositionID),
                ),
        )
        
        msg := tgbotapi.NewMessage(userID, text)
        msg.ReplyMarkup = keyboard
        msg.ParseMode = "Markdown"
        tb.Bot.Send(msg)
}

// Helper methods
func (tb *TelegramBot) sendMessage(chatID int64, text string) {
        msg := tgbotapi.NewMessage(chatID, text)
        msg.ParseMode = "Markdown"
        tb.Bot.Send(msg)
}

// sendMessageWithMenu sends a message with persistent menu
func (tb *TelegramBot) sendMessageWithMenu(chatID int64, text string) {
        msg := tgbotapi.NewMessage(chatID, text)
        msg.ParseMode = "Markdown"
        
        // Create persistent keyboard menu
        keyboard := tgbotapi.NewReplyKeyboard(
                tgbotapi.NewKeyboardButtonRow(
                        tgbotapi.NewKeyboardButton("📊 Pozisyonlar"),
                        tgbotapi.NewKeyboardButton("💰 Bakiye"),
                ),
                tgbotapi.NewKeyboardButtonRow(
                        tgbotapi.NewKeyboardButton("⚙️ Ayarlar"),
                        tgbotapi.NewKeyboardButton("🧪 Test"),
                ),
                tgbotapi.NewKeyboardButtonRow(
                        tgbotapi.NewKeyboardButton("📝 Kayıt Ol"),
                        tgbotapi.NewKeyboardButton("🔑 API Güncelle"),
                ),
                tgbotapi.NewKeyboardButtonRow(
                        tgbotapi.NewKeyboardButton("❓ Yardım"),
                        tgbotapi.NewKeyboardButton("🏠 Ana Sayfa"),
                ),
        )
        
        keyboard.ResizeKeyboard = true
        keyboard.OneTimeKeyboard = false
        
        msg.ReplyMarkup = keyboard
        tb.Bot.Send(msg)
}

func (tb *TelegramBot) getUser(userID int64) (*models.User, error) {
        var user models.User
        err := database.DB.Where("telegram_id = ?", userID).First(&user).Error
        if err != nil {
                return nil, err
        }
        return &user, nil
}

func (tb *TelegramBot) getUserState(userID int64) *UserState {
        if state, exists := userStates[userID]; exists {
                return state
        }
        state := &UserState{State: "none", Data: make(map[string]interface{})}
        userStates[userID] = state
        return state
}

func (tb *TelegramBot) setUserState(userID int64, state string, data map[string]interface{}) {
        if data == nil {
                data = make(map[string]interface{})
        }
        userStates[userID] = &UserState{State: state, Data: data}
}

func (tb *TelegramBot) clearUserState(userID int64) {
        delete(userStates, userID)
}

// Additional handlers for commands
func (tb *TelegramBot) handleStatusCommand(chatID int64, userID int64) {
        user, err := tb.getUser(userID)
        if err != nil {
                tb.sendMessage(chatID, "❌ Önce /register ile kayıt olmanız gerekiyor.")
                return
        }
        
        // Get user positions
        var positions []models.Position
        err = database.DB.Where("user_id = ? AND status = ?", user.ID, models.PositionOpen).Find(&positions).Error
        if err != nil {
                tb.sendMessage(chatID, "❌ Pozisyonlar yüklenirken hata oluştu.")
                return
        }
        
        if len(positions) == 0 {
                tb.sendMessage(chatID, "📊 Şu anda aktif pozisyonunuz bulunmuyor.")
                return
        }
        
        text := "📊 *Aktif Pozisyonlarınız:*\n\n"
        for _, pos := range positions {
                text += fmt.Sprintf("💰 %s\n📊 Entry: $%.6f\n🎯 TP: $%.6f\n💵 P&L: $%.2f\n\n", 
                        pos.Symbol, pos.EntryPrice, pos.TakeProfitPrice, pos.CurrentPNL)
        }
        
        tb.sendMessage(chatID, text)
}

func (tb *TelegramBot) handleBalanceCommand(chatID int64, userID int64) {
        user, err := tb.getUser(userID)
        if err != nil {
                tb.sendMessage(chatID, "❌ Önce /register ile kayıt olmanız gerekiyor.")
                return
        }
        
        // Get API credentials and check balance
        apiKey, apiSecret, passphrase, err := user.GetAPICredentials(tb.EncryptionKey)
        if err != nil {
                tb.sendMessage(chatID, "❌ API anahtarları alınamadı. Lütfen /register ile tekrar girin.")
                return
        }
        
        bitgetAPI := NewBitgetAPI(apiKey, apiSecret, passphrase)
        balances, err := bitgetAPI.GetAccountBalance()
        if err != nil {
                tb.sendMessage(chatID, "❌ Bakiye bilgisi alınamadı. API anahtarlarınızı kontrol edin.")
                return
        }
        
        // Format balance information
        text := "💰 *Bitget Futures Bakiyeniz:*\n\n"
        
        if len(balances) == 0 {
                text += "❌ Hiç bakiye bulunamadı."
        } else {
                for _, balance := range balances {
                        text += fmt.Sprintf("🪙 *%s:*\n", balance.MarginCoin)
                        text += fmt.Sprintf("💵 Available: %s\n", balance.Available)
                        text += fmt.Sprintf("🔒 Locked: %s\n", balance.Locked)
                        text += fmt.Sprintf("📊 Equity: %s\n", balance.Equity)
                        text += fmt.Sprintf("💎 USDT Equity: %s\n", balance.USDTEquity)
                        text += "\n"
                }
        }
        
        tb.sendMessage(chatID, text)
}

func (tb *TelegramBot) handleTestCommand(chatID int64, userID int64) {
        // Check if user is registered
        user, err := tb.getUser(userID)
        if err != nil {
                tb.sendMessage(chatID, "❌ Önce /register ile kayıt olmanız gerekiyor.")
                return
        }
        
        // Check if user is active
        if !user.IsActive {
                tb.sendMessage(chatID, "❌ Hesabınız aktif değil. /settings ile aktif hale getirin.")
                return
        }
        
        // Show test options
        text := "🧪 *TEST MODU*\n\nHangi coin ile test yapmak istiyorsunuz?\n\n" +
                "⚠️ *DİKKAT:* Bu gerçek API kullanır!\n" +
                "Test coin Bitget'te mevcut olmalı.\n\n" +
                "Örnek test coinleri:\n" +
                "• BTC (Bitcoin)\n" +
                "• ETH (Ethereum)\n" +
                "• SOL (Solana)\n" +
                "• DOGE (Dogecoin)"

        keyboard := tgbotapi.NewInlineKeyboardMarkup(
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("🪙 Test BTC", "test_BTC"),
                        tgbotapi.NewInlineKeyboardButtonData("💎 Test ETH", "test_ETH"),
                ),
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("🌟 Test SOL", "test_SOL"),
                        tgbotapi.NewInlineKeyboardButtonData("🐕 Test DOGE", "test_DOGE"),
                ),
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("🔢 Custom Coin", "test_custom"),
                ),
        )

        msg := tgbotapi.NewMessage(chatID, text)
        msg.ReplyMarkup = keyboard
        msg.ParseMode = "Markdown"
        tb.Bot.Send(msg)
}

func (tb *TelegramBot) handleHelpCommand(chatID int64) {
        helpText := `❓ *Yardım - Bot Rehberi*

🎛️ *Menü Komutları:*
📊 Pozisyonlar - Aktif pozisyonları görüntüle
💰 Bakiye - Hesap bakiyesini sorgula
⚙️ Ayarlar - Trading ayarlarını düzenle
🧪 Test - Test trade işlemi yap
📝 Kayıt Ol - API anahtarlarını kaydet
🔑 API Güncelle - API bilgilerini güncelle
🏠 Ana Sayfa - Bot anasayfasına dön

📊 *Bot Nasıl Çalışır:*
1. 🔍 Upbit duyurularını sürekli takip eder
2. 🆕 Yeni coin listelerini tespit eder  
3. 📈 Bitget'te otomatik long pozisyon açar
4. 💹 Her 3dk'da P&L güncellemelerini yapar
5. 🎯 Take profit seviyesinde otomatik kapatır

🔧 *İlk Kurulum:*
1. 📝 Kayıt Ol → API anahtarlarınızı girin
2. ⚙️ Ayarlar → Miktar, leverage, TP ayarlayın
3. 🚀 Bot otomatik çalışmaya başlar!

⚠️ *Önemli:* Bu bot gerçek para ile işlem yapar!

💡 *İpucu:* Menüden istediğiniz komutu seçebilirsiniz!`
        
        tb.sendMessageWithMenu(chatID, helpText)
}

// handleUpdateAPICommand handles /update_api command
func (tb *TelegramBot) handleUpdateAPICommand(chatID int64, userID int64) {
        // Check if user exists
        _, err := tb.getUser(userID)
        if err != nil {
                tb.sendMessageWithMenu(chatID, "❌ Önce /register komutu ile kayıt olmanız gerekiyor.")
                return
        }
        
        confirmText := `🔑 *API Bilgilerini Güncelle*

⚠️ *DİKKAT:* Mevcut API bilgileriniz silinecek ve yenileri kaydedilecek.

📝 Bitget'ten yeni API bilgilerinizi hazır bulundurun:
• API Key
• Secret Key  
• Passphrase

✅ Devam etmek istiyorsanız **YENİ API KEY**'inizi gönderin.
❌ İptal etmek için /cancel yazın.`

        tb.sendMessage(chatID, confirmText)
        tb.setUserState(userID, "awaiting_update_api_key", nil)
}

// handleUpdateAPIKeyInput handles updated API key input
func (tb *TelegramBot) handleUpdateAPIKeyInput(chatID int64, userID int64, apiKey string) {
        if apiKey == "/cancel" {
                tb.clearUserState(userID)
                tb.sendMessageWithMenu(chatID, "❌ API güncelleme iptal edildi.")
                return
        }
        
        // Clean whitespace
        apiKey = strings.TrimSpace(apiKey)
        
        // Validate API key format (more flexible validation)
        if len(apiKey) < 10 || len(apiKey) > 100 {
                tb.sendMessage(chatID, "❌ Geçersiz API key formatı. API key 10-100 karakter arası olmalı.\nLütfen tekrar deneyin:")
                return
        }
        
        // Log for debugging (first few chars only)
        log.Printf("🔑 API Key update - User: %d, Key prefix: %s...", userID, apiKey[:min(len(apiKey), 8)])
        
        // Store temporarily in user state
        tb.setUserState(userID, "awaiting_update_api_secret", map[string]interface{}{
                "api_key": apiKey,
        })
        
        tb.sendMessage(chatID, "✅ API Key kaydedildi.\n\n🔐 Şimdi **SECRET KEY**'inizi gönderin:")
}

// handleUpdateAPISecretInput handles updated API secret input
func (tb *TelegramBot) handleUpdateAPISecretInput(chatID int64, userID int64, apiSecret string) {
        if apiSecret == "/cancel" {
                tb.clearUserState(userID)
                tb.sendMessageWithMenu(chatID, "❌ API güncelleme iptal edildi.")
                return
        }
        
        // Clean whitespace
        apiSecret = strings.TrimSpace(apiSecret)
        
        // Validate API secret format (more flexible validation)
        if len(apiSecret) < 10 || len(apiSecret) > 150 {
                tb.sendMessage(chatID, "❌ Geçersiz secret key formatı. Secret key 10-150 karakter arası olmalı.\nLütfen tekrar deneyin:")
                return
        }
        
        // Get API key from state with error handling
        state := tb.getUserState(userID)
        if state.Data == nil {
                log.Printf("❌ State data is nil for user %d", userID)
                tb.clearUserState(userID)
                tb.sendMessageWithMenu(chatID, "❌ Session hatası. Lütfen 🔑 API Güncelle ile tekrar başlayın.")
                return
        }
        
        apiKeyInterface, exists := state.Data["api_key"]
        if !exists {
                log.Printf("❌ API key not found in state for user %d", userID)
                tb.clearUserState(userID)
                tb.sendMessageWithMenu(chatID, "❌ API key bulunamadı. Lütfen 🔑 API Güncelle ile tekrar başlayın.")
                return
        }
        
        apiKey, ok := apiKeyInterface.(string)
        if !ok {
                log.Printf("❌ API key type assertion failed for user %d", userID)
                tb.clearUserState(userID)
                tb.sendMessageWithMenu(chatID, "❌ Session hatası. Lütfen 🔑 API Güncelle ile tekrar başlayın.")
                return
        }
        
        // Log for debugging
        log.Printf("🔐 Secret Key update - User: %d, Secret prefix: %s...", userID, apiSecret[:min(len(apiSecret), 8)])
        
        // Store both in state
        tb.setUserState(userID, "awaiting_update_passphrase", map[string]interface{}{
                "api_key":    apiKey,
                "api_secret": apiSecret,
        })
        
        tb.sendMessage(chatID, "✅ Secret Key kaydedildi.\n\n🔑 Son olarak **PASSPHRASE**'inizi gönderin:")
}

// handleUpdatePassphraseInput handles updated passphrase input and saves everything
func (tb *TelegramBot) handleUpdatePassphraseInput(chatID int64, userID int64, passphrase string) {
        if passphrase == "/cancel" {
                tb.clearUserState(userID)
                tb.sendMessageWithMenu(chatID, "❌ API güncelleme iptal edildi.")
                return
        }
        
        // Clean whitespace
        passphrase = strings.TrimSpace(passphrase)
        
        // Validate passphrase (more flexible validation)
        if len(passphrase) < 3 || len(passphrase) > 50 {
                tb.sendMessage(chatID, "❌ Geçersiz passphrase formatı. Passphrase 3-50 karakter arası olmalı.\nLütfen tekrar deneyin:")
                return
        }
        
        // Get API credentials from state with error handling
        state := tb.getUserState(userID)
        if state.Data == nil {
                log.Printf("❌ State data is nil for user %d in passphrase step", userID)
                tb.clearUserState(userID)
                tb.sendMessageWithMenu(chatID, "❌ Session hatası. Lütfen 🔑 API Güncelle ile tekrar başlayın.")
                return
        }
        
        // Get API key with type checking
        apiKeyInterface, exists := state.Data["api_key"]
        if !exists {
                log.Printf("❌ API key not found in state for user %d in passphrase step", userID)
                tb.clearUserState(userID)
                tb.sendMessageWithMenu(chatID, "❌ API key bulunamadı. Lütfen 🔑 API Güncelle ile tekrar başlayın.")
                return
        }
        
        apiKey, ok := apiKeyInterface.(string)
        if !ok {
                log.Printf("❌ API key type assertion failed for user %d in passphrase step", userID)
                tb.clearUserState(userID)
                tb.sendMessageWithMenu(chatID, "❌ Session hatası. Lütfen 🔑 API Güncelle ile tekrar başlayın.")
                return
        }
        
        // Get API secret with type checking
        apiSecretInterface, exists := state.Data["api_secret"]
        if !exists {
                log.Printf("❌ API secret not found in state for user %d in passphrase step", userID)
                tb.clearUserState(userID)
                tb.sendMessageWithMenu(chatID, "❌ API secret bulunamadı. Lütfen 🔑 API Güncelle ile tekrar başlayın.")
                return
        }
        
        apiSecret, ok := apiSecretInterface.(string)
        if !ok {
                log.Printf("❌ API secret type assertion failed for user %d in passphrase step", userID)
                tb.clearUserState(userID)
                tb.sendMessageWithMenu(chatID, "❌ Session hatası. Lütfen 🔑 API Güncelle ile tekrar başlayın.")
                return
        }
        
        // Log for debugging
        log.Printf("🔑 Complete API update - User: %d, Key: %s..., Secret: %s..., Pass: %s...", 
                userID, apiKey[:min(len(apiKey), 8)], apiSecret[:min(len(apiSecret), 8)], passphrase[:min(len(passphrase), 3)])
        
        // Get user from database
        user, err := tb.getUser(userID)
        if err != nil {
                log.Printf("❌ User not found in database for user %d: %v", userID, err)
                tb.clearUserState(userID)
                tb.sendMessageWithMenu(chatID, "❌ Kullanıcı bulunamadı. Lütfen /register ile tekrar kayıt olun.")
                return
        }
        
        // Update API credentials
        err = user.UpdateAPICredentials(apiKey, apiSecret, passphrase, tb.EncryptionKey)
        if err != nil {
                log.Printf("❌ API credentials update failed for user %d: %v", userID, err)
                tb.clearUserState(userID)
                tb.sendMessage(chatID, fmt.Sprintf("❌ API bilgileri güncellenirken hata: %v", err))
                return
        }
        
        // Save updated user
        if err := database.DB.Save(user).Error; err != nil {
                log.Printf("❌ Database save failed for user %d: %v", userID, err)
                tb.clearUserState(userID)
                tb.sendMessage(chatID, fmt.Sprintf("❌ Database kaydetme hatası: %v", err))
                return
        }
        
        log.Printf("✅ API credentials successfully updated for user %d", userID)
        
        // Clear user state
        tb.clearUserState(userID)
        
        // Send success message
        successText := `✅ *API Bilgileri Başarıyla Güncellendi!*

🔐 Yeni API anahtarlarınız şifrelenerek kaydedildi.

📊 Test etmek için:
• 💰 Bakiye - Hesap durumunu kontrol edin
• 🧪 Test - Test trade yaparak doğrulayın

⚙️ Diğer ayarlarınız (miktar, leverage, TP) değişmedi.`

        tb.sendMessageWithMenu(chatID, successText)
}

// Callback handlers
func (tb *TelegramBot) handleClosePositionCallback(chatID int64, userID int64, positionID string) {
        // Get position details
        var position models.Position
        err := database.DB.Where("position_id = ? AND user_id = (SELECT id FROM users WHERE telegram_id = ?)", 
                positionID, userID).First(&position).Error
        if err != nil {
                tb.sendMessage(chatID, "❌ Pozisyon bulunamadı.")
                return
        }
        
        // Store position ID in user state for confirmation callback
        tb.setUserState(userID, "confirming_close", map[string]interface{}{
                "position_id": positionID,
        })
        
        // Show confirmation dialog
        message := fmt.Sprintf("🚨 *Pozisyonu Kapat*\n\n"+
                "💰 Symbol: %s\n"+
                "📊 Quantity: %.6f\n"+
                "💵 Entry Price: $%.2f\n"+
                "📈 Current Price: $%.2f\n"+
                "💸 P&L: $%.2f (%.2f%%)\n\n"+
                "⚠️ Pozisyonu kapatmak istediğinizden emin misiniz?",
                position.Symbol, position.Quantity, position.EntryPrice, 
                position.CurrentPrice, position.CurrentPNL, position.ROE)
        
        keyboard := tgbotapi.NewInlineKeyboardMarkup(
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("✅ Evet, Kapat", "confirm_close"),
                        tgbotapi.NewInlineKeyboardButtonData("❌ İptal", "cancel_close"),
                ),
        )
        
        msg := tgbotapi.NewMessage(chatID, message)
        msg.ParseMode = "Markdown"
        msg.ReplyMarkup = keyboard
        tb.Bot.Send(msg)
}

func (tb *TelegramBot) handleConfirmCloseCallback(chatID int64, userID int64) {
        tb.sendMessage(chatID, "✅ Pozisyon kapatma talebi alındı. İşlem gerçekleştiriliyor...")
        
        // Get stored position ID from user state
        state := tb.getUserState(userID)
        if state == nil || state.Data == nil {
                tb.sendMessage(chatID, "❌ Pozisyon bilgisi bulunamadı.")
                return
        }
        
        positionIDStr, ok := state.Data["position_id"].(string)
        if !ok {
                tb.sendMessage(chatID, "❌ Pozisyon ID bulunamadı.")
                return
        }
        
        // Get position from database
        var position models.Position
        err := database.DB.Where("position_id = ? AND user_id = (SELECT id FROM users WHERE telegram_id = ?)", 
                positionIDStr, userID).First(&position).Error
        if err != nil {
                tb.sendMessage(chatID, "❌ Pozisyon bulunamadı.")
                return
        }
        
        // Get user for API credentials
        user, err := tb.getUser(userID)
        if err != nil {
                tb.sendMessage(chatID, "❌ Kullanıcı bilgileri alınamadı.")
                return
        }
        
        // Get user's API credentials
        apiKey, apiSecret, passphrase, err := user.GetAPICredentials(tb.EncryptionKey)
        if err != nil {
                tb.sendMessage(chatID, "❌ API bilgileri alınamadı.")
                return
        }
        
        // Initialize Bitget API
        bitgetAPI := NewBitgetAPI(apiKey, apiSecret, passphrase)
        
        // Close position on Bitget using Flash Close (market price instantly)
        log.Printf("🚨 EMERGENCY CLOSE: Flash closing position %s for user %d", position.PositionID, userID)
        
        // Try flash close first (ONLY for this specific position)
        orderResp, err := bitgetAPI.FlashClosePosition(position.Symbol, "long")
        if err != nil {
                // Check if error is "No position to close" - means position already closed
                if strings.Contains(err.Error(), "22002") || strings.Contains(err.Error(), "No position to close") {
                        log.Printf("ℹ️ Position %s already closed on Bitget, updating database", position.PositionID)
                        
                        // Position already closed on Bitget, just update our database
                        now := time.Now()
                        position.Status = models.PositionClosed
                        position.ClosedAt = &now
                        
                        if err := database.DB.Save(&position).Error; err != nil {
                                log.Printf("❌ Failed to update position in database: %v", err)
                                tb.sendMessage(chatID, "❌ Pozisyon database'de güncellenemedi.")
                                return
                        }
                        
                        tb.sendMessage(chatID, "✅ Pozisyon zaten kapatılmıştı! Database güncellendi.")
                        tb.clearUserState(userID)
                        return
                }
                
                // CRITICAL FIX: Do NOT use CloseAllPositions as fallback!
                // This would close ALL user positions, not just the requested one
                log.Printf("❌ Flash close failed for position %s: %v", position.PositionID, err)
                tb.sendMessage(chatID, fmt.Sprintf("❌ Pozisyon kapatılamadı: %v\n\n⚠️ UYARI: Sadece bu pozisyon kapanmadı, diğer pozisyonlarınız güvende.", err))
                tb.clearUserState(userID)
                return
        }
        
        // Update position status in database
        now := time.Now()
        position.Status = models.PositionClosed
        position.ClosedAt = &now
        
        if err := database.DB.Save(&position).Error; err != nil {
                log.Printf("❌ Failed to update position in database: %v", err)
        }
        
        log.Printf("✅ Position closed successfully: order ID %s", orderResp.OrderID)
        tb.sendMessage(chatID, fmt.Sprintf("✅ Pozisyon başarıyla kapatıldı!\n📝 Close Order ID: %s", orderResp.OrderID))
        
        // Clear user state
        tb.clearUserState(userID)
}

func (tb *TelegramBot) handleCancelCloseCallback(chatID int64) {
        tb.sendMessage(chatID, "❌ Pozisyon kapatma işlemi iptal edildi.")
}

func (tb *TelegramBot) handleTradeAmountCallback(chatID int64, userID int64, amount string) {
        text := "💰 *Trade Amount Seçin (USDT):*"
        
        keyboard := tgbotapi.NewInlineKeyboardMarkup(
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("20 USDT", "amount_20"),
                        tgbotapi.NewInlineKeyboardButtonData("50 USDT", "amount_50"),
                ),
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("100 USDT", "amount_100"),
                        tgbotapi.NewInlineKeyboardButtonData("200 USDT", "amount_200"),
                ),
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("500 USDT", "amount_500"),
                        tgbotapi.NewInlineKeyboardButtonData("🔢 Custom", "amount_custom"),
                ),
        )
        
        msg := tgbotapi.NewMessage(chatID, text)
        msg.ReplyMarkup = keyboard
        msg.ParseMode = "Markdown"
        tb.Bot.Send(msg)
}

func (tb *TelegramBot) handleLeverageCallback(chatID int64, userID int64, leverage string) {
        text := "🔧 *Leverage Seçin:*"
        
        keyboard := tgbotapi.NewInlineKeyboardMarkup(
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("5x", "leverage_5"),
                        tgbotapi.NewInlineKeyboardButtonData("10x", "leverage_10"),
                ),
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("20x", "leverage_20"),
                        tgbotapi.NewInlineKeyboardButtonData("50x", "leverage_50"),
                ),
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("🔢 Custom (1-125)", "leverage_custom"),
                ),
        )
        
        msg := tgbotapi.NewMessage(chatID, text)
        msg.ReplyMarkup = keyboard
        msg.ParseMode = "Markdown"
        tb.Bot.Send(msg)
}

func (tb *TelegramBot) handleTakeProfitCallback(chatID int64, userID int64, takeProfit string) {
        text := "📈 *Take Profit Seçin (%):*"
        
        keyboard := tgbotapi.NewInlineKeyboardMarkup(
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("100%", "tp_100"),
                        tgbotapi.NewInlineKeyboardButtonData("200%", "tp_200"),
                ),
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("300%", "tp_300"),
                        tgbotapi.NewInlineKeyboardButtonData("500%", "tp_500"),
                ),
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("🔢 Custom", "tp_custom"),
                ),
        )
        
        msg := tgbotapi.NewMessage(chatID, text)
        msg.ReplyMarkup = keyboard
        msg.ParseMode = "Markdown"
        tb.Bot.Send(msg)
}

// New callback handlers for specific selections
func (tb *TelegramBot) handleAmountSelectionCallback(chatID int64, userID int64, amount string) {
        if amount == "custom" {
                tb.sendMessage(chatID, "💰 *Custom Trade Amount*\n\nLütfen trade amount'ı USDT cinsinden girin:\n(Örnek: 150)")
                tb.setUserState(userID, "awaiting_trade_amount", nil)
                return
        }
        
        // Parse predefined amounts
        var amountValue float64
        switch amount {
        case "20": amountValue = 20
        case "50": amountValue = 50
        case "100": amountValue = 100
        case "200": amountValue = 200
        case "500": amountValue = 500
        default:
                tb.sendMessage(chatID, "❌ Geçersiz amount seçimi.")
                return
        }
        
        user, err := tb.getUser(userID)
        if err != nil {
                tb.sendMessage(chatID, "❌ Kullanıcı bulunamadı.")
                return
        }
        
        user.TradeAmount = amountValue
        if err := database.DB.Save(user).Error; err != nil {
                tb.sendMessage(chatID, "❌ Ayar kaydedilirken hata oluştu.")
                return
        }
        
        tb.sendMessage(chatID, fmt.Sprintf("✅ Trade amount %.0f USDT olarak güncellendi.", amountValue))
}

func (tb *TelegramBot) handleLeverageSelectionCallback(chatID int64, userID int64, leverage string) {
        if leverage == "custom" {
                tb.sendMessage(chatID, "🔧 *Custom Leverage*\n\nLütfen leverage değerini girin (1-125):\n(Örnek: 15)")
                tb.setUserState(userID, "awaiting_leverage", nil)
                return
        }
        
        // Parse predefined leverages
        var leverageValue int
        switch leverage {
        case "5": leverageValue = 5
        case "10": leverageValue = 10
        case "20": leverageValue = 20
        case "50": leverageValue = 50
        default:
                tb.sendMessage(chatID, "❌ Geçersiz leverage seçimi.")
                return
        }
        
        user, err := tb.getUser(userID)
        if err != nil {
                tb.sendMessage(chatID, "❌ Kullanıcı bulunamadı.")
                return
        }
        
        user.Leverage = leverageValue
        if err := database.DB.Save(user).Error; err != nil {
                tb.sendMessage(chatID, "❌ Ayar kaydedilirken hata oluştu.")
                return
        }
        
        tb.sendMessage(chatID, fmt.Sprintf("✅ Leverage %dx olarak güncellendi.", leverageValue))
}

func (tb *TelegramBot) handleTakeProfitSelectionCallback(chatID int64, userID int64, takeProfit string) {
        if takeProfit == "custom" {
                tb.sendMessage(chatID, "📈 *Custom Take Profit*\n\nLütfen take profit yüzdesini girin:\n(Örnek: 250 -> %250)")
                tb.setUserState(userID, "awaiting_take_profit", nil)
                return
        }
        
        // Parse predefined take profits
        var takeProfitValue float64
        switch takeProfit {
        case "100": takeProfitValue = 100
        case "200": takeProfitValue = 200
        case "300": takeProfitValue = 300
        case "500": takeProfitValue = 500
        default:
                tb.sendMessage(chatID, "❌ Geçersiz take profit seçimi.")
                return
        }
        
        user, err := tb.getUser(userID)
        if err != nil {
                tb.sendMessage(chatID, "❌ Kullanıcı bulunamadı.")
                return
        }
        
        user.TakeProfitPercentage = takeProfitValue
        if err := database.DB.Save(user).Error; err != nil {
                tb.sendMessage(chatID, "❌ Ayar kaydedilirken hata oluştu.")
                return
        }
        
        tb.sendMessage(chatID, fmt.Sprintf("✅ Take profit %.0f%% olarak güncellendi.", takeProfitValue))
}

func (tb *TelegramBot) handleTestCoinCallback(chatID int64, userID int64, coinSymbol string) {
        if coinSymbol == "custom" {
                tb.sendMessage(chatID, "🧪 *Custom Test Coin*\n\nLütfen test etmek istediğiniz coin symbol'ını girin:\n(Örnek: AVAX, LINK, UNI)")
                tb.setUserState(userID, "awaiting_test_coin", nil)
                return
        }
        
        // Execute test trade for the selected coin
        tb.executeTestTrade(chatID, userID, coinSymbol)
}

func (tb *TelegramBot) executeTestTrade(chatID int64, userID int64, coinSymbol string) {
        if tb.upbitMonitor == nil {
                tb.sendMessage(chatID, "❌ Test sistemi kullanılamıyor.")
                return
        }
        
        confirmText := fmt.Sprintf(`🧪 *TEST TRADİNG ONAY*

🪙 Test Coin: %s
⚠️ *DİKKAT:* Bu gerçek para ile işlem yapar!

Test yapmak istediğinizden emin misiniz?`, coinSymbol)

        keyboard := tgbotapi.NewInlineKeyboardMarkup(
                tgbotapi.NewInlineKeyboardRow(
                        tgbotapi.NewInlineKeyboardButtonData("✅ Test Et", fmt.Sprintf("confirm_test_%s", coinSymbol)),
                        tgbotapi.NewInlineKeyboardButtonData("❌ İptal", "cancel_test"),
                ),
        )

        msg := tgbotapi.NewMessage(chatID, confirmText)
        msg.ReplyMarkup = keyboard
        msg.ParseMode = "Markdown"
        tb.Bot.Send(msg)
}

func (tb *TelegramBot) handleConfirmTestCallback(chatID int64, userID int64, coinSymbol string) {
        tb.sendMessage(chatID, fmt.Sprintf("🧪 Test başlatılıyor: %s\n\nSadece sizin API anahtarınızla test ediliyor...", coinSymbol))
        
        // Inject test coin ONLY for this user
        if tb.upbitMonitor != nil {
                tb.upbitMonitor.InjectTestCoinForUser(coinSymbol, userID)
                tb.sendMessage(chatID, "✅ Test coin injection başarılı! Sadece sizin hesabınızda test ediliyor.")
        } else {
                tb.sendMessage(chatID, "❌ Upbit monitor mevcut değil.")
        }
}

func (tb *TelegramBot) handleToggleActiveCallback(chatID int64, userID int64) {
        user, err := tb.getUser(userID)
        if err != nil {
                tb.sendMessage(chatID, "❌ Kullanıcı bulunamadı.")
                return
        }
        
        user.IsActive = !user.IsActive
        if err := database.DB.Save(user).Error; err != nil {
                tb.sendMessage(chatID, "❌ Ayar kaydedilirken hata oluştu.")
                return
        }
        
        status := "Pasif"
        if user.IsActive {
                status = "Aktif"
        }
        
        tb.sendMessage(chatID, fmt.Sprintf("✅ Bot durumu güncellendi: %s", status))
}

// Input handlers for settings
func (tb *TelegramBot) handleTradeAmountInput(chatID int64, userID int64, input string) {
        amount, err := strconv.ParseFloat(input, 64)
        if err != nil || amount <= 0 {
                tb.sendMessage(chatID, "❌ Geçersiz miktar. Lütfen pozitif bir sayı girin.")
                return
        }
        
        user, err := tb.getUser(userID)
        if err != nil {
                tb.sendMessage(chatID, "❌ Kullanıcı bulunamadı.")
                tb.clearUserState(userID)
                return
        }
        
        user.TradeAmount = amount
        if err := database.DB.Save(user).Error; err != nil {
                tb.sendMessage(chatID, "❌ Ayar kaydedilirken hata oluştu.")
                tb.clearUserState(userID)
                return
        }
        
        tb.sendMessage(chatID, fmt.Sprintf("✅ Trade amount %.0f USDT olarak güncellendi.", amount))
        tb.clearUserState(userID)
}

func (tb *TelegramBot) handleLeverageInput(chatID int64, userID int64, input string) {
        leverage, err := strconv.Atoi(input)
        if err != nil || leverage < 1 || leverage > 125 {
                tb.sendMessage(chatID, "❌ Geçersiz leverage. 1-125 arasında bir değer girin.")
                return
        }
        
        user, err := tb.getUser(userID)
        if err != nil {
                tb.sendMessage(chatID, "❌ Kullanıcı bulunamadı.")
                tb.clearUserState(userID)
                return
        }
        
        user.Leverage = leverage
        if err := database.DB.Save(user).Error; err != nil {
                tb.sendMessage(chatID, "❌ Ayar kaydedilirken hata oluştu.")
                tb.clearUserState(userID)
                return
        }
        
        tb.sendMessage(chatID, fmt.Sprintf("✅ Leverage %dx olarak güncellendi.", leverage))
        tb.clearUserState(userID)
}

func (tb *TelegramBot) handleTakeProfitInput(chatID int64, userID int64, input string) {
        takeProfit, err := strconv.ParseFloat(input, 64)
        if err != nil || takeProfit <= 0 {
                tb.sendMessage(chatID, "❌ Geçersiz take profit. Pozitif bir yüzde değeri girin.")
                return
        }
        
        user, err := tb.getUser(userID)
        if err != nil {
                tb.sendMessage(chatID, "❌ Kullanıcı bulunamadı.")
                tb.clearUserState(userID)
                return
        }
        
        user.TakeProfitPercentage = takeProfit
        if err := database.DB.Save(user).Error; err != nil {
                tb.sendMessage(chatID, "❌ Ayar kaydedilirken hata oluştu.")
                tb.clearUserState(userID)
                return
        }
        
        tb.sendMessage(chatID, fmt.Sprintf("✅ Take profit %.0f%% olarak güncellendi.", takeProfit))
        tb.clearUserState(userID)
}
