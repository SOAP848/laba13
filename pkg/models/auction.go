package models

import "time"

// Типы сообщений для аукциона
const (
	MessageTypeAuctionAnnounce = "auction.announce"
	MessageTypeAuctionBid      = "auction.bid"
	MessageTypeAuctionResult   = "auction.result"
)

// Объявление аукциона (задача)
type AuctionAnnounce struct {
	BaseMessage
	TaskID       string                 `json:"task_id"`
	Requirements map[string]interface{} `json:"requirements"` // язык, сложность, deadline и т.д.
	Deadline     time.Time              `json:"deadline"`     // время окончания приёма ставок
	MinScore     float64                `json:"min_score"`    // минимальный score для участия
}

// Ставка агента
type AuctionBid struct {
	BaseMessage
	AuctionID     string    `json:"auction_id"`
	AgentID       string    `json:"agent_id"`
	Cost          float64   `json:"cost"`           // стоимость выполнения (меньше лучше)
	SkillScore    float64   `json:"skill_score"`    // соответствие задаче (0..1)
	Availability  float64   `json:"availability"`   // доступность (0..1)
	EstimatedTime int       `json:"estimated_time"` // оценка времени в секундах
	BidTimestamp  time.Time `json:"bid_timestamp"`
}

// Результат аукциона
type AuctionResult struct {
	BaseMessage
	AuctionID     string     `json:"auction_id"`
	WinnerAgentID string     `json:"winner_agent_id"`
	WinnerBid     AuctionBid `json:"winner_bid"`
	TotalBids     int        `json:"total_bids"`
	DecisionTime  time.Time  `json:"decision_time"`
}

// Вычисление score ставки (комбинированная метрика)
func (b *AuctionBid) CalculateScore() float64 {
	// Пример: чем меньше cost и больше skill/availability, тем лучше
	// Формула: score = (skillScore * availability) / (cost + 1)
	return (b.SkillScore * b.Availability) / (b.Cost + 1.0)
}
