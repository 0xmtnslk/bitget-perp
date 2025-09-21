# Upbit-Bitget Crypto Trading Bot System

## Overview

This is a comprehensive cryptocurrency trading bot system built with Go that automatically detects newly listed coins on Upbit exchange and opens long positions on Bitget futures market. The system features a multi-user Telegram bot interface that allows users to configure their trading parameters and monitor their positions in real-time.

The bot monitors Upbit's announcement page continuously for new coin listings, extracts coin symbols from announcements, and automatically executes trades on Bitget futures using user-configured parameters like trade amount, leverage, and take profit levels.

## User Preferences

Preferred communication style: Simple, everyday language.

## System Architecture

### Backend Architecture
- **Language**: Go for high-performance concurrent operations
- **Database**: PostgreSQL with Drizzle ORM for user data and trading configuration storage
- **Connection Pool**: Neon serverless PostgreSQL with WebSocket support

### Core Modules

**Upbit Monitoring Module**
- Continuously scrapes Upbit announcements (https://upbit.com/service_center/notice)
- Parses announcements to extract new coin symbols using regex patterns
- Implements duplicate detection to prevent reprocessing the same coins
- Operates on 10-30 second intervals for real-time detection

**Bitget Trading Engine**
- Integrates with Bitget API for USDT-M futures trading
- Handles market order placement, position monitoring, and automated take profit orders
- Manages user-specific API credentials and trading parameters
- Implements position tracking and balance management

**Telegram Bot Interface**
- Multi-user support with individual user configurations
- Command-based interface for registration, settings management, and status monitoring
- Secure storage of user API credentials with encryption
- Real-time notifications for trade executions and position updates

### Data Layer
**User Management Schema**
- Stores encrypted API credentials (key, secret, passphrase)
- Maintains user trading preferences (amount, leverage, take profit percentage)
- Tracks user activation status and registration timestamps
- Implements proper encryption for sensitive financial data

**Security Architecture**
- API credentials stored with encryption
- User isolation for trading operations
- Secure WebSocket connections for database operations

### Trading Logic
- Automatic long position opening on detected new listings
- Configurable leverage options (5x, 10x, 20x, 50x)
- Flexible trade amounts (20, 50, 100, 200, 500 USDT)
- Automated take profit execution (100%, 200%, 300%, 500%)
- Position monitoring and management

## External Dependencies

**Cryptocurrency Exchanges**
- Upbit API/Web scraping for new coin announcements
- Bitget Futures API for trade execution and position management

**Database Services**
- Neon PostgreSQL serverless database
- WebSocket connections for real-time data operations

**Communication Platform**
- Telegram Bot API for user interface and notifications

**Development Tools**
- Drizzle ORM for type-safe database operations
- WebSocket library for serverless database connections

**Security Services**
- Encryption libraries for API credential storage
- Secure connection protocols for external API communications
