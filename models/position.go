package models

import (
	"time"
	"gorm.io/gorm"
)

type PositionStatus string

const (
	PositionOpen   PositionStatus = "open"
	PositionClosed PositionStatus = "closed"
)

type Position struct {
	ID             uint           `json:"id" gorm:"primaryKey"`
	PositionID     string         `json:"position_id" gorm:"uniqueIndex;size:100"` // Bitget position ID
	UserID         uint           `json:"user_id" gorm:"not null"`
	CoinSymbol     string         `json:"coin_symbol" gorm:"size:20;not null"`      // TOSHI, OPEN, etc.
	Symbol         string         `json:"symbol" gorm:"size:30;not null"`           // TOSHIUSDT, OPENUSDT
	EntryPrice     float64        `json:"entry_price" gorm:"type:decimal(20,8)"`
	CurrentPrice   float64        `json:"current_price" gorm:"type:decimal(20,8)"`
	Quantity       float64        `json:"quantity" gorm:"type:decimal(20,8)"`
	Leverage       int            `json:"leverage"`
	TakeProfitPrice float64       `json:"take_profit_price" gorm:"type:decimal(20,8)"`
	CurrentPNL     float64        `json:"current_pnl" gorm:"type:decimal(20,8);default:0"`
	ROE            float64        `json:"roe" gorm:"type:decimal(10,4);default:0"` // Return on Equity %
	Status         PositionStatus `json:"status" gorm:"type:varchar(20);default:'open'"`
	OpenedAt       time.Time      `json:"opened_at"`
	ClosedAt       *time.Time     `json:"closed_at,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	
	// Relations
	User User `json:"user,omitempty" gorm:"foreignKey:UserID"`
}

// CalculatePNL calculates the current P&L and ROE
func (p *Position) CalculatePNL() {
	if p.EntryPrice <= 0 || p.CurrentPrice <= 0 || p.Quantity <= 0 {
		return
	}
	
	// For long positions: PNL = (current_price - entry_price) * quantity
	priceDiff := p.CurrentPrice - p.EntryPrice
	p.CurrentPNL = priceDiff * p.Quantity
	
	// ROE = (PNL / margin) * 100
	// Margin = (entry_price * quantity) / leverage
	margin := (p.EntryPrice * p.Quantity) / float64(p.Leverage)
	if margin > 0 {
		p.ROE = (p.CurrentPNL / margin) * 100
	}
}

// ShouldTakeProfit checks if position should be closed for take profit
func (p *Position) ShouldTakeProfit() bool {
	return p.Status == PositionOpen && p.CurrentPrice >= p.TakeProfitPrice
}

// BeforeCreate GORM hook
func (p *Position) BeforeCreate(tx *gorm.DB) error {
	p.OpenedAt = time.Now()
	return nil
}
