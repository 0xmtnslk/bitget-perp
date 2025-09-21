package services

import (
        "fmt"
        "log"
        "math/rand"
        "net/http"
        "net/url"
        "os"
        "regexp"
        "strconv"
        "strings"
        "sync"
        "time"

        "github.com/PuerkitoBio/goquery"
)

// Initialize random seed for jitter
func init() {
        rand.Seed(time.Now().UnixNano())
}

// UpbitMonitor monitors Upbit announcements for new coin listings
type UpbitMonitor struct {
        checkInterval   time.Duration
        processedCoins  map[string]bool
        coinMutex      sync.RWMutex
        newCoinChannel chan string
        testCoinChannel chan string  // For user-specific test coins
        stopChannel    chan bool
        // Rate limiting fields
        lastETag       string        // For conditional GET requests
        lastModified   string        // For conditional GET requests
        backoffUntil   time.Time     // Exponential backoff timestamp
        failureCount   int           // Consecutive failure count for backoff
        httpClient     *http.Client  // Reusable HTTP client with potential proxy
}

// CoinListing represents a detected coin listing
type CoinListing struct {
        Symbol      string
        AnnouncementTitle string
        DetectedAt  time.Time
        Markets     []string // KRW, USDT markets
}

// NewUpbitMonitor creates a new Upbit monitor instance
func NewUpbitMonitor(checkInterval time.Duration) *UpbitMonitor {
        // Create HTTP client with optional proxy support
        client := &http.Client{
                Timeout: 30 * time.Second,
        }
        
        // Check for proxy configuration
        if proxyURL := os.Getenv("UPBIT_PROXY_URL"); proxyURL != "" {
                if proxy, err := url.Parse(proxyURL); err == nil {
                        client.Transport = &http.Transport{
                                Proxy: http.ProxyURL(proxy),
                        }
                        log.Printf("🌐 Using proxy for Upbit requests: %s", proxyURL)
                } else {
                        log.Printf("⚠️ Invalid proxy URL: %s", proxyURL)
                }
        }
        
        return &UpbitMonitor{
                checkInterval:   checkInterval,
                processedCoins:  make(map[string]bool),
                coinMutex:      sync.RWMutex{},
                newCoinChannel: make(chan string, 100),
                testCoinChannel: make(chan string, 10),  // Smaller buffer for tests
                stopChannel:    make(chan bool),
                httpClient:     client,
        }
}

// Start begins monitoring Upbit announcements with improved rate limiting
func (um *UpbitMonitor) Start() {
        log.Printf("🚀 Starting Upbit monitor - checking every %v with jitter", um.checkInterval)
        
        // Initial check
        um.checkAnnouncements()
        
        for {
                // Calculate next check time with jitter (±10% randomness)
                jitter := time.Duration(float64(um.checkInterval) * (0.9 + rand.Float64()*0.2))
                timer := time.NewTimer(jitter)
                
                select {
                case <-timer.C:
                        um.checkAnnouncements()
                case <-um.stopChannel:
                        timer.Stop()
                        log.Println("🛑 Upbit monitor stopped")
                        return
                }
                timer.Stop()
        }
}

// Stop stops the monitoring service
func (um *UpbitMonitor) Stop() {
        um.stopChannel <- true
}

// GetNewCoinChannel returns the channel for new coin notifications
func (um *UpbitMonitor) GetNewCoinChannel() <-chan string {
        return um.newCoinChannel
}

// GetTestCoinChannel returns the channel for test coin notifications  
func (um *UpbitMonitor) GetTestCoinChannel() <-chan string {
        return um.testCoinChannel
}

// InjectTestCoin manually injects a test coin for debugging/testing purposes
func (um *UpbitMonitor) InjectTestCoin(coinSymbol string) {
        log.Printf("🧪 MANUAL TEST: Injecting test coin: %s", coinSymbol)
        
        // Check if already processed to avoid duplicates
        um.coinMutex.Lock()
        if um.processedCoins[coinSymbol] {
                log.Printf("⚠️ Test coin %s already processed, skipping", coinSymbol)
                um.coinMutex.Unlock()
                return
        }
        
        // Mark as processed and send to channel
        um.processedCoins[coinSymbol] = true
        um.coinMutex.Unlock()
        
        // Send to trading engine via channel
        select {
        case um.newCoinChannel <- coinSymbol:
                log.Printf("✅ Test coin %s sent to trading engine", coinSymbol)
        default:
                log.Printf("⚠️ Channel full, could not inject test coin %s", coinSymbol)
        }
}

// InjectTestCoinForUser manually injects a test coin for a specific user only
func (um *UpbitMonitor) InjectTestCoinForUser(coinSymbol string, userID int64) {
        log.Printf("🧪 USER TEST: Injecting test coin %s for user %d only", coinSymbol, userID)
        
        // Send test coin with user ID to a special test channel 
        testData := fmt.Sprintf("%s:%d", coinSymbol, userID)
        
        select {
        case um.testCoinChannel <- testData:
                log.Printf("✅ Test coin %s sent for user %d", coinSymbol, userID)
        default:
                log.Printf("⚠️ Test channel full, could not inject test coin %s for user %d", coinSymbol, userID)
        }
}

// checkAnnouncements scrapes Upbit announcements page with rate limiting and caching
func (um *UpbitMonitor) checkAnnouncements() {
        // Check if we're in backoff period
        if time.Now().Before(um.backoffUntil) {
                log.Printf("⏳ In backoff period until %v, skipping check", um.backoffUntil.Format("15:04:05"))
                return
        }
        
        log.Println("🔍 Checking Upbit announcements...")
        
        req, err := http.NewRequest("GET", "https://upbit.com/service_center/notice", nil)
        if err != nil {
                log.Printf("❌ Failed to create request: %v", err)
                um.handleError()
                return
        }
        
        // Set proper headers to mimic browser
        req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
        req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
        req.Header.Set("Accept-Language", "en-US,en;q=0.5")
        
        // Add conditional GET headers if we have cached data
        if um.lastETag != "" {
                req.Header.Set("If-None-Match", um.lastETag)
        }
        if um.lastModified != "" {
                req.Header.Set("If-Modified-Since", um.lastModified)
        }
        
        resp, err := um.httpClient.Do(req)
        if err != nil {
                log.Printf("❌ Failed to fetch Upbit announcements: %v", err)
                um.handleError()
                return
        }
        defer resp.Body.Close()
        
        // Handle rate limiting and other HTTP errors
        switch resp.StatusCode {
        case 200:
                // Success - reset failure count
                um.failureCount = 0
                
                // Cache ETag and Last-Modified for next request
                if etag := resp.Header.Get("ETag"); etag != "" {
                        um.lastETag = etag
                }
                if lastModified := resp.Header.Get("Last-Modified"); lastModified != "" {
                        um.lastModified = lastModified
                }
                
                // Parse HTML
                doc, err := goquery.NewDocumentFromReader(resp.Body)
                if err != nil {
                        log.Printf("❌ Failed to parse HTML: %v", err)
                        um.handleError()
                        return
                }
                
                // Extract announcements
                um.parseAnnouncements(doc)
                
        case 304:
                // Not Modified - page hasn't changed, this is good!
                log.Println("📄 Page not modified since last check (304)")
                um.failureCount = 0
                
        case 429:
                // Too Many Requests - honor Retry-After if provided, otherwise exponential backoff
                retryAfter := resp.Header.Get("Retry-After")
                if retryAfter != "" {
                        if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
                                retryDuration := time.Duration(seconds) * time.Second
                                um.backoffUntil = time.Now().Add(retryDuration)
                                log.Printf("🚫 Rate limited by Upbit (429) - honoring Retry-After: %v", retryDuration)
                        } else {
                                log.Printf("🚫 Rate limited by Upbit (429) - invalid Retry-After, applying exponential backoff")
                                um.applyBackoff()
                        }
                } else {
                        log.Printf("🚫 Rate limited by Upbit (429) - applying exponential backoff")
                        um.applyBackoff()
                }
                
        case 403:
                // Forbidden - might be IP blocked
                log.Printf("🚫 Access forbidden by Upbit (403) - possible IP block, applying backoff")
                um.applyBackoff()
                
        default:
                log.Printf("❌ Upbit returned status code: %d", resp.StatusCode)
                um.handleError()
        }
}

// handleError handles general errors with light backoff
func (um *UpbitMonitor) handleError() {
        um.failureCount++
        if um.failureCount >= 3 {
                // Apply light backoff after 3 consecutive failures
                backoffDuration := time.Duration(um.failureCount) * 30 * time.Second
                um.backoffUntil = time.Now().Add(backoffDuration)
                log.Printf("⚠️ %d consecutive failures, backing off for %v", um.failureCount, backoffDuration)
        }
}

// applyBackoff applies exponential backoff for rate limiting
func (um *UpbitMonitor) applyBackoff() {
        um.failureCount++
        
        // Exponential backoff: 1, 2, 4, 8, 10 minutes max
        backoffMinutes := 1 << uint(um.failureCount-1)
        if backoffMinutes > 10 {
                backoffMinutes = 10
        }
        
        backoffDuration := time.Duration(backoffMinutes) * time.Minute
        um.backoffUntil = time.Now().Add(backoffDuration)
        
        log.Printf("📉 Applying exponential backoff for %v (failure #%d)", backoffDuration, um.failureCount)
}

// parseAnnouncements extracts coin symbols from announcement titles
func (um *UpbitMonitor) parseAnnouncements(doc *goquery.Document) {
        foundNewCoins := false
        
        // Look for announcement titles (adjust selector based on actual HTML structure)
        doc.Find(".notice-list-item, .announcement-item, a[href*='notice']").Each(func(i int, s *goquery.Selection) {
                title := strings.TrimSpace(s.Text())
                
                if title == "" {
                        return
                }
                
                // Detect market support announcements
                if um.isMarketSupportAnnouncement(title) {
                        coins := um.extractCoinSymbols(title)
                        
                        for _, coin := range coins {
                                if um.isNewCoin(coin) {
                                        log.Printf("🎯 NEW COIN DETECTED: %s from announcement: %s", coin, title)
                                        um.markCoinAsProcessed(coin)
                                        
                                        // Send to channel for trading processing
                                        select {
                                        case um.newCoinChannel <- coin:
                                                foundNewCoins = true
                                        default:
                                                log.Printf("⚠️ New coin channel full, dropping coin: %s", coin)
                                        }
                                }
                        }
                }
        })
        
        if !foundNewCoins {
                log.Println("📊 No new coins detected in current check")
        }
}

// isMarketSupportAnnouncement checks if title indicates market support announcement
func (um *UpbitMonitor) isMarketSupportAnnouncement(title string) bool {
        lowerTitle := strings.ToLower(title)
        
        // Korean patterns
        koreanPatterns := []string{
                "마켓 지원",
                "거래 지원", 
                "상장",
                "신규 상장",
                "원화 마켓",
                "usdt 마켓",
        }
        
        // English patterns
        englishPatterns := []string{
                "market support",
                "trading support",
                "listing",
                "new listing",
                "krw market",
                "usdt market",
                "support for",
        }
        
        allPatterns := append(koreanPatterns, englishPatterns...)
        
        for _, pattern := range allPatterns {
                if strings.Contains(lowerTitle, pattern) {
                        return true
                }
        }
        
        return false
}

// extractCoinSymbols extracts coin symbols from announcement title
func (um *UpbitMonitor) extractCoinSymbols(title string) []string {
        var coins []string
        
        // Pattern 1: "Market Support for Toshi(TOSHI) (KRW, USDT Market)"
        re1 := regexp.MustCompile(`\(([A-Z]+)\)`)
        matches1 := re1.FindAllStringSubmatch(title, -1)
        for _, match := range matches1 {
                if len(match) > 1 {
                        symbol := strings.ToUpper(strings.TrimSpace(match[1]))
                        if um.isValidCoinSymbol(symbol) {
                                coins = append(coins, symbol)
                        }
                }
        }
        
        // Pattern 2: Direct symbol mentions like "TOSHI 거래 지원"
        re2 := regexp.MustCompile(`\b([A-Z]{2,10})\b`)
        matches2 := re2.FindAllStringSubmatch(title, -1)
        for _, match := range matches2 {
                if len(match) > 1 {
                        symbol := strings.ToUpper(strings.TrimSpace(match[1]))
                        if um.isValidCoinSymbol(symbol) && !um.isCommonWord(symbol) {
                                coins = append(coins, symbol)
                        }
                }
        }
        
        return um.removeDuplicates(coins)
}

// isValidCoinSymbol checks if a symbol looks like a valid cryptocurrency symbol
func (um *UpbitMonitor) isValidCoinSymbol(symbol string) bool {
        // Basic validation: 2-10 characters, all uppercase letters/numbers
        if len(symbol) < 2 || len(symbol) > 10 {
                return false
        }
        
        // Must be mostly letters
        letterCount := 0
        for _, char := range symbol {
                if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
                        if char >= 'A' && char <= 'Z' {
                                letterCount++
                        }
                } else {
                        return false
                }
        }
        
        // At least 50% letters
        return letterCount >= len(symbol)/2
}

// isCommonWord filters out common English words that aren't crypto symbols
func (um *UpbitMonitor) isCommonWord(word string) bool {
        commonWords := map[string]bool{
                "FOR": true, "THE": true, "AND": true, "WITH": true, "MARKET": true,
                "SUPPORT": true, "NEW": true, "TRADING": true, "KRW": true, "USDT": true,
                "USD": true, "BTC": true, "ETH": true, "ANNOUNCEMENT": true,
        }
        
        return commonWords[word]
}

// isNewCoin checks if coin hasn't been processed before
func (um *UpbitMonitor) isNewCoin(symbol string) bool {
        um.coinMutex.RLock()
        defer um.coinMutex.RUnlock()
        
        return !um.processedCoins[symbol]
}

// markCoinAsProcessed marks a coin as already processed
func (um *UpbitMonitor) markCoinAsProcessed(symbol string) {
        um.coinMutex.Lock()
        defer um.coinMutex.Unlock()
        
        um.processedCoins[symbol] = true
}

// removeDuplicates removes duplicate symbols from slice
func (um *UpbitMonitor) removeDuplicates(symbols []string) []string {
        seen := make(map[string]bool)
        var result []string
        
        for _, symbol := range symbols {
                if !seen[symbol] {
                        seen[symbol] = true
                        result = append(result, symbol)
                }
        }
        
        return result
}

// GetProcessedCoins returns list of processed coins (for testing/debugging)
func (um *UpbitMonitor) GetProcessedCoins() []string {
        um.coinMutex.RLock()
        defer um.coinMutex.RUnlock()
        
        var coins []string
        for coin := range um.processedCoins {
                coins = append(coins, coin)
        }
        
        return coins
}

// ClearProcessedCoins clears the processed coins list (for testing)
func (um *UpbitMonitor) ClearProcessedCoins() {
        um.coinMutex.Lock()
        defer um.coinMutex.Unlock()
        
        um.processedCoins = make(map[string]bool)
}
