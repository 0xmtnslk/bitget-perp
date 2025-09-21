package services

import (
        "bytes"
        "crypto/hmac"
        "crypto/sha256"
        "encoding/base64"
        "encoding/json"
        "fmt"
        "io"
        "net/http"
        "net/url"
        "strconv"
        "strings"
        "time"
)

// BitgetAPI handles Bitget USDT-M Futures API operations
type BitgetAPI struct {
        APIKey     string
        APISecret  string
        Passphrase string
        BaseURL    string
        Client     *http.Client
}

// OrderSide represents order side
type OrderSide string

const (
        OrderSideBuy  OrderSide = "buy"
        OrderSideSell OrderSide = "sell"
)

// OrderType represents order type
type OrderType string

const (
        OrderTypeMarket OrderType = "market"
        OrderTypeLimit  OrderType = "limit"
)

// PositionSide represents position side
type PositionSide string

const (
        PositionSideLong  PositionSide = "long"
        PositionSideShort PositionSide = "short"
)

// BitgetPosition represents a Bitget futures position
type BitgetPosition struct {
        PositionID      string  `json:"positionId"`
        Symbol          string  `json:"symbol"`
        Size            string  `json:"size"`
        Side            string  `json:"side"`
        MarkPrice       string  `json:"markPrice"`
        EntryPrice      string  `json:"entryPrice"`
        UnrealizedPL    string  `json:"unrealizedPL"`
        Leverage        string  `json:"leverage"`
        MarginSize      string  `json:"marginSize"`
        LiquidationPrice string `json:"liquidationPrice"`
        CreatedAt       string  `json:"cTime"`
        UpdatedAt       string  `json:"uTime"`
}

// OrderRequest represents an order request for Bitget v2 API (official documentation)
type OrderRequest struct {
        Symbol      string    `json:"symbol"`      // Trading pair, e.g. ETHUSDT
        ProductType string    `json:"productType"` // USDT-FUTURES, COIN-FUTURES, USDC-FUTURES
        MarginMode  string    `json:"marginMode"`  // isolated or crossed
        MarginCoin  string    `json:"marginCoin"`  // Margin coin (capitalized)
        Size        string    `json:"size"`        // Amount (base coin)
        Side        OrderSide `json:"side"`        // buy or sell
        TradeSide   string    `json:"tradeSide,omitempty"` // open or close (hedge-mode only)
        OrderType   OrderType `json:"orderType"`   // limit or market
        Price       string    `json:"price,omitempty"`
        Force       string    `json:"force,omitempty"`       // gtc, ioc, fok, post_only
        ClientOID   string    `json:"clientOid,omitempty"`
        ReduceOnly  string    `json:"reduceOnly,omitempty"`  // YES or NO
}

// OrderResponse represents order response
type OrderResponse struct {
        OrderID   string `json:"orderId"`
        ClientOID string `json:"clientOid"`
}

// APIResponse represents standard Bitget API response
type APIResponse struct {
        Code      string      `json:"code"`
        Message   string      `json:"msg"`
        RequestID interface{} `json:"requestTime"` // Can be string or number
        Data      interface{} `json:"data"`
}

// NewBitgetAPI creates a new Bitget API client
func NewBitgetAPI(apiKey, apiSecret, passphrase string) *BitgetAPI {
        return &BitgetAPI{
                APIKey:     apiKey,
                APISecret:  apiSecret,
                Passphrase: passphrase,
                BaseURL:    "https://api.bitget.com",
                Client: &http.Client{
                        Timeout: 30 * time.Second,
                },
        }
}

// PlaceOrder places a futures market order using official v2 API
func (b *BitgetAPI) PlaceOrder(symbol string, side OrderSide, size float64, tradeSide string) (*OrderResponse, error) {
        orderReq := OrderRequest{
                Symbol:      symbol,
                ProductType: "USDT-FUTURES",   // USDT-M Futures
                MarginMode:  "isolated",       // Isolated margin
                MarginCoin:  "USDT",          // Margin coin (capitalized)
                Size:        fmt.Sprintf("%.8f", size),
                Side:        side,            // buy or sell
                TradeSide:   tradeSide,       // open or close
                OrderType:   OrderTypeMarket, // market order
                Force:       "gtc",           // Good till canceled
        }
        
        endpoint := "/api/v2/mix/order/place-order"
        
        fmt.Printf("🚀 Placing v2 order: %+v\n", orderReq)
        
        var orderResp OrderResponse
        err := b.makeRequest("POST", endpoint, orderReq, &orderResp)
        if err != nil {
                fmt.Printf("❌ Order placement failed: %v\n", err)
                return nil, fmt.Errorf("failed to place order: %w", err)
        }
        
        fmt.Printf("✅ Order placed successfully: %+v\n", orderResp)
        return &orderResp, nil
}

// OpenLongPosition opens a long position like Python version
func (b *BitgetAPI) OpenLongPosition(symbol string, marginUSDT float64, leverage int) (*OrderResponse, error) {
        // First set leverage
        if err := b.SetLeverage(symbol, leverage); err != nil {
                return nil, fmt.Errorf("failed to set leverage: %w", err)
        }
        
        // Get current price to calculate proper size
        currentPrice, err := b.GetSymbolPrice(symbol)
        if err != nil {
                return nil, fmt.Errorf("failed to get current price: %w", err)
        }
        
        // Python logic: For USDT-M futures, calculate size based on margin and leverage
        // Total position value = marginUSDT * leverage
        // Size in base currency = (marginUSDT * leverage) / currentPrice
        totalPositionValue := marginUSDT * float64(leverage)
        baseSize := totalPositionValue / currentPrice
        
        fmt.Printf("📊 Opening long position: symbol=%s, margin=%.2f USDT, leverage=%dx, price=%.6f, total_value=%.2f, size=%.8f\n", 
                symbol, marginUSDT, leverage, currentPrice, totalPositionValue, baseSize)
        
        return b.PlaceOrder(symbol, OrderSideBuy, baseSize, "open")
}

// FlashClosePosition closes position using flash close API (market price instantly)
func (b *BitgetAPI) FlashClosePosition(symbol string, holdSide string) (*OrderResponse, error) {
        endpoint := "/api/v2/mix/order/close-positions"
        
        closeReq := map[string]interface{}{
                "symbol":      symbol,
                "productType": "USDT-FUTURES",
                "holdSide":    holdSide, // "long" or "short"
        }
        
        fmt.Printf("🚨 Flash closing position: %+v\n", closeReq)
        
        var response map[string]interface{}
        err := b.makeRequestWithRetry("POST", endpoint, nil, closeReq, &response)
        if err != nil {
                fmt.Printf("❌ Flash close failed: %v\n", err)
                return nil, fmt.Errorf("failed to flash close position: %w", err)
        }
        
        // Parse response - check both APIResponse wrapper and direct response
        var data map[string]interface{}
        
        if apiResp, ok := response["data"].(map[string]interface{}); ok {
                // Direct response format
                data = apiResp
        } else {
                // Could be wrapped in APIResponse format, check raw response
                data = response
        }
        
        fmt.Printf("🔍 Flash close response data: %+v\n", data)
        
        // Check for successful closes
        successList, ok := data["successList"].([]interface{})
        if !ok || len(successList) == 0 {
                // Check failure list for errors
                if failureList, ok := data["failureList"].([]interface{}); ok && len(failureList) > 0 {
                        failure := failureList[0].(map[string]interface{})
                        errorMsg, _ := failure["errorMsg"].(string)
                        return nil, fmt.Errorf("flash close failed: %s", errorMsg)
                }
                return nil, fmt.Errorf("no successful closes in response")
        }
        
        // Get first successful close
        success := successList[0].(map[string]interface{})
        orderResp := &OrderResponse{
                OrderID:   fmt.Sprintf("%v", success["orderId"]),
                ClientOID: fmt.Sprintf("%v", success["clientOid"]),
        }
        
        fmt.Printf("✅ Flash close successful: %+v\n", orderResp)
        return orderResp, nil
}

// CloseAllPositions closes all positions for USDT-FUTURES product type
func (b *BitgetAPI) CloseAllPositions() (*OrderResponse, error) {
        endpoint := "/api/v2/mix/order/close-positions"
        
        closeReq := map[string]interface{}{
                "productType": "USDT-FUTURES", // Close all USDT futures positions
        }
        
        fmt.Printf("🚨 Closing ALL USDT-FUTURES positions\n")
        
        var response map[string]interface{}
        err := b.makeRequestWithRetry("POST", endpoint, nil, closeReq, &response)
        if err != nil {
                fmt.Printf("❌ Close all positions failed: %v\n", err)
                return nil, fmt.Errorf("failed to close all positions: %w", err)
        }
        
        // Parse response - check both APIResponse wrapper and direct response  
        var data map[string]interface{}
        
        if apiResp, ok := response["data"].(map[string]interface{}); ok {
                // Direct response format
                data = apiResp
        } else {
                // Could be wrapped in APIResponse format, check raw response
                data = response
        }
        
        fmt.Printf("🔍 Close all response data: %+v\n", data)
        
        // Check for successful closes
        successList, ok := data["successList"].([]interface{})
        if !ok || len(successList) == 0 {
                // Check failure list for errors
                if failureList, ok := data["failureList"].([]interface{}); ok && len(failureList) > 0 {
                        failure := failureList[0].(map[string]interface{})
                        errorMsg, _ := failure["errorMsg"].(string)
                        return nil, fmt.Errorf("close all failed: %s", errorMsg)
                }
                return nil, fmt.Errorf("no positions to close")
        }
        
        // Get first successful close (could be multiple)
        success := successList[0].(map[string]interface{})
        orderResp := &OrderResponse{
                OrderID:   fmt.Sprintf("%v", success["orderId"]),
                ClientOID: fmt.Sprintf("%v", success["clientOid"]),
        }
        
        fmt.Printf("✅ All positions closed successfully: %d closed\n", len(successList))
        return orderResp, nil
}

// ClosePosition closes a position by placing opposite order (legacy method)
func (b *BitgetAPI) ClosePosition(symbol string, size float64, side PositionSide) (*OrderResponse, error) {
        // Try flash close first for long positions 
        if side == PositionSideLong {
                return b.FlashClosePosition(symbol, "long")
        }
        
        // Fallback to regular order method
        var orderSide OrderSide
        if side == PositionSideLong {
                orderSide = OrderSideSell
        } else {
                orderSide = OrderSideBuy
        }
        
        return b.PlaceOrder(symbol, orderSide, size, "close")
}

// GetPosition gets current position for a symbol
func (b *BitgetAPI) GetPosition(symbol string) (*BitgetPosition, error) {
        endpoint := "/api/v2/mix/position/single-position"
        params := map[string]string{
                "symbol":       symbol,
                "marginCoin":   "USDT",
        }
        
        var positions []BitgetPosition
        err := b.makeRequestWithParams("GET", endpoint, params, nil, &positions)
        if err != nil {
                return nil, fmt.Errorf("failed to get position: %w", err)
        }
        
        if len(positions) == 0 {
                return nil, fmt.Errorf("no position found for symbol: %s", symbol)
        }
        
        return &positions[0], nil
}

// GetAllPositions gets all open positions
func (b *BitgetAPI) GetAllPositions() ([]BitgetPosition, error) {
        endpoint := "/api/v2/mix/position/all-position"
        params := map[string]string{
                "productType": "usdt-futures",
                "marginCoin":  "USDT",
        }
        
        var positions []BitgetPosition
        err := b.makeRequestWithParams("GET", endpoint, params, nil, &positions)
        if err != nil {
                return nil, fmt.Errorf("failed to get all positions: %w", err)
        }
        
        return positions, nil
}

// SetLeverage sets leverage for a symbol using v2 API
func (b *BitgetAPI) SetLeverage(symbol string, leverage int) error {
        endpoint := "/api/v2/mix/account/set-leverage"
        
        leverageReq := map[string]interface{}{
                "symbol":      symbol,
                "productType": "USDT-FUTURES",
                "marginCoin":  "USDT",
                "leverage":    strconv.Itoa(leverage),
        }
        
        fmt.Printf("⚡ Setting leverage %dx for %s\n", leverage, symbol)
        
        var response interface{}
        err := b.makeRequest("POST", endpoint, leverageReq, &response)
        if err != nil {
                return fmt.Errorf("failed to set leverage: %w", err)
        }
        
        fmt.Printf("✅ Leverage set successfully\n")
        return nil
}

// AccountBalance represents account balance information
type AccountBalance struct {
        MarginCoin        string `json:"marginCoin"`
        Locked            string `json:"locked"`
        Available         string `json:"available"`
        CrossMaxAvailable string `json:"crossMaxAvailable"`
        FixedMaxAvailable string `json:"fixedMaxAvailable"`
        MaxTransferOut    string `json:"maxTransferOut"`
        Equity            string `json:"equity"`
        USDTEquity        string `json:"usdtEquity"`
        BonusAmount       string `json:"bonusAmount"`
}

// GetAccountBalance gets account balance using v2 API 
func (b *BitgetAPI) GetAccountBalance() ([]AccountBalance, error) {
        fmt.Printf("🔍 Getting account balance from Bitget v2 API...\n")
        
        endpoint := "/api/v2/mix/account/accounts"
        params := map[string]string{
                "productType": "USDT-FUTURES",
        }
        
        fmt.Printf("📡 API Endpoint: %s\n", endpoint)
        fmt.Printf("📊 Params: %+v\n", params)
        // Removed sensitive API credentials from logs for security
        fmt.Printf("🔐 API Request initiated\n")
        
        var balances []AccountBalance
        err := b.makeRequestWithParams("GET", endpoint, params, nil, &balances)
        if err != nil {
                fmt.Printf("❌ Balance API Error: %v\n", err)
                return nil, fmt.Errorf("failed to get account balance: %w", err)
        }
        
        fmt.Printf("✅ Balance response received: %+v\n", balances)
        return balances, nil
}

// GetSymbolPrice gets current symbol price using v2 API
func (b *BitgetAPI) GetSymbolPrice(symbol string) (float64, error) {
        endpoint := "/api/v2/mix/market/ticker"
        params := map[string]string{
                "symbol":      symbol,
                "productType": "USDT-FUTURES",  // Required parameter for v2 API
        }
        
        fmt.Printf("🔍 Getting price for symbol: %s\n", symbol)
        
        // Build query string
        values := url.Values{}
        for k, v := range params {
                values.Add(k, v)
        }
        queryString := values.Encode()
        
        // Build full URL
        fullURL := b.BaseURL + endpoint + "?" + queryString
        
        // Create HTTP request
        req, err := http.NewRequest("GET", fullURL, nil)
        if err != nil {
                return 0, fmt.Errorf("failed to create request: %w", err)
        }
        
        // Set headers
        timestamp := strconv.FormatInt(time.Now().UnixNano()/1e6, 10)
        signaturePath := endpoint + "?" + queryString
        
        req.Header.Set("ACCESS-KEY", b.APIKey)
        req.Header.Set("ACCESS-SIGN", b.generateSignature("GET", signaturePath, "", timestamp))
        req.Header.Set("ACCESS-PASSPHRASE", b.Passphrase)
        req.Header.Set("ACCESS-TIMESTAMP", timestamp)
        req.Header.Set("locale", "en-US")
        req.Header.Set("Content-Type", "application/json")
        
        // Make request
        resp, err := b.Client.Do(req)
        if err != nil {
                return 0, fmt.Errorf("failed to make request: %w", err)
        }
        defer resp.Body.Close()
        
        // Read response
        respBody, err := io.ReadAll(resp.Body)
        if err != nil {
                return 0, fmt.Errorf("failed to read response: %w", err)
        }
        
        fmt.Printf("🔍 HTTP Status: %d\n", resp.StatusCode)
        // Removed raw response logging to reduce noise
        fmt.Printf("🔍 API Response received\n")
        
        // Parse response directly without APIResponse wrapper
        var directResponse map[string]interface{}
        if err := json.Unmarshal(respBody, &directResponse); err != nil {
                return 0, fmt.Errorf("failed to parse response: %w", err)
        }
        
        // Check response code
        code, ok := directResponse["code"].(string)
        if !ok || code != "00000" {
                msg, _ := directResponse["msg"].(string)
                return 0, fmt.Errorf("API error: %s - %s", code, msg)
        }
        
        // Parse data array
        data, ok := directResponse["data"].([]interface{})
        if !ok || len(data) == 0 {
                return 0, fmt.Errorf("invalid response format or no data")
        }
        
        // Get first ticker item
        tickerData, ok := data[0].(map[string]interface{})
        if !ok {
                return 0, fmt.Errorf("invalid ticker data format")
        }
        
        // Get price
        priceStr, ok := tickerData["lastPr"].(string)
        if !ok {
                return 0, fmt.Errorf("lastPr field not found")
        }
        
        price, err := strconv.ParseFloat(priceStr, 64)
        if err != nil {
                return 0, fmt.Errorf("failed to parse price: %w", err)
        }
        
        fmt.Printf("📊 Current price for %s: $%.2f\n", symbol, price)
        return price, nil
}

// makeRequest makes authenticated HTTP request
// makeRequestWithRetry makes authenticated HTTP request with retry logic for rate limiting
func (b *BitgetAPI) makeRequestWithRetry(method, endpoint string, params map[string]string, body interface{}, result interface{}) error {
        maxRetries := 3
        baseDelay := time.Second * 2
        
        for attempt := 0; attempt <= maxRetries; attempt++ {
                err := b.makeRequestWithParams(method, endpoint, params, body, result)
                
                // If no error, return success
                if err == nil {
                        return nil
                }
                
                // Check if it's a rate limit error
                if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "Too Many Requests") {
                        if attempt < maxRetries {
                                delay := time.Duration(1<<uint(attempt)) * baseDelay // Exponential backoff
                                fmt.Printf("⏰ Rate limited, retrying in %v... (attempt %d/%d)\n", delay, attempt+1, maxRetries+1)
                                time.Sleep(delay)
                                continue
                        }
                }
                
                // Return error if not rate limit or max retries reached
                return err
        }
        
        return fmt.Errorf("max retries exceeded")
}

func (b *BitgetAPI) makeRequest(method, endpoint string, body interface{}, result interface{}) error {
        return b.makeRequestWithRetry(method, endpoint, nil, body, result)
}

// makeRequestWithParams makes authenticated HTTP request with query parameters
func (b *BitgetAPI) makeRequestWithParams(method, endpoint string, params map[string]string, body interface{}, result interface{}) error {
        // Build query string
        var queryString string
        if params != nil && len(params) > 0 {
                values := url.Values{}
                for k, v := range params {
                        values.Add(k, v)
                }
                queryString = values.Encode()
        }
        
        // Build full URL
        fullURL := b.BaseURL + endpoint
        if queryString != "" {
                fullURL += "?" + queryString
        }
        
        // Prepare request body
        var reqBody []byte
        var err error
        if body != nil {
                reqBody, err = json.Marshal(body)
                if err != nil {
                        return fmt.Errorf("failed to marshal request body: %w", err)
                }
        }
        
        // Create HTTP request
        req, err := http.NewRequest(method, fullURL, bytes.NewReader(reqBody))
        if err != nil {
                return fmt.Errorf("failed to create request: %w", err)
        }
        
        // Set headers
        timestamp := strconv.FormatInt(time.Now().UnixNano()/1e6, 10)
        
        // Build signature path (endpoint + query string for GET requests)
        signaturePath := endpoint
        if method == "GET" && queryString != "" {
                signaturePath += "?" + queryString
        }
        
        // Set headers exactly like official documentation
        req.Header.Set("ACCESS-KEY", b.APIKey)
        req.Header.Set("ACCESS-SIGN", b.generateSignature(method, signaturePath, string(reqBody), timestamp))
        req.Header.Set("ACCESS-PASSPHRASE", b.Passphrase)
        req.Header.Set("ACCESS-TIMESTAMP", timestamp)
        req.Header.Set("locale", "en-US")
        req.Header.Set("Content-Type", "application/json")
        
        // Make request
        resp, err := b.Client.Do(req)
        if err != nil {
                return fmt.Errorf("failed to make request: %w", err)
        }
        defer resp.Body.Close()
        
        // Read response
        respBody, err := io.ReadAll(resp.Body)
        if err != nil {
                return fmt.Errorf("failed to read response: %w", err)
        }
        
        fmt.Printf("🔍 HTTP Status: %d\n", resp.StatusCode)
        // Removed raw response logging to reduce noise
        fmt.Printf("🔍 API Response received\n")
        
        // Parse API response
        var apiResp APIResponse
        if err := json.Unmarshal(respBody, &apiResp); err != nil {
                return fmt.Errorf("failed to parse API response: %w", err)
        }
        
        fmt.Printf("🔍 Parsed API Response: Code=%s, Message=%s\n", apiResp.Code, apiResp.Message)
        
        // Check for API errors
        if apiResp.Code != "00000" {
                return fmt.Errorf("API error: %s - %s", apiResp.Code, apiResp.Message)
        }
        
        // Parse result data
        if result != nil {
                dataBytes, err := json.Marshal(apiResp.Data)
                if err != nil {
                        return fmt.Errorf("failed to marshal response data: %w", err)
                }
                
                if err := json.Unmarshal(dataBytes, result); err != nil {
                        return fmt.Errorf("failed to unmarshal result: %w", err)
                }
        }
        
        return nil
}

// generateSignature generates request signature for authentication
func (b *BitgetAPI) generateSignature(method, endpoint, body, timestamp string) string {
        // Build signature string
        message := timestamp + method + endpoint + body
        
        // Removed signature message logging for security
        
        // Create HMAC-SHA256 signature
        h := hmac.New(sha256.New, []byte(b.APISecret))
        h.Write([]byte(message))
        signature := base64.StdEncoding.EncodeToString(h.Sum(nil))
        
        // Removed signature logging for security
        
        return signature
}

// FormatSymbol formats coin symbol for Bitget v2 futures (e.g., BTC -> BTCUSDT)
func (b *BitgetAPI) FormatSymbol(coinSymbol string) string {
        return strings.ToUpper(coinSymbol) + "USDT"
}

// IsSymbolValid checks if symbol exists on Bitget
func (b *BitgetAPI) IsSymbolValid(symbol string) bool {
        price, err := b.GetSymbolPrice(symbol)
        if err != nil {
                fmt.Printf("❌ IsSymbolValid failed for %s: %v\n", symbol, err)
                return false
        }
        fmt.Printf("✅ IsSymbolValid success for %s: price=$%.2f\n", symbol, price)
        return true
}
