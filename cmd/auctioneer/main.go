package main

import (
	"context" 
	"encoding/json"
	"os"
	"os/signal"
	"syscall"
	"time"

	"test-automation-agents/internal/common"
	"test-automation-agents/internal/natsclient"
	"test-automation-agents/pkg/models"

	natsio "github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
)

const (
	serviceName      = "auctioneer"
	natsURL          = "nats://localhost:4222"
	otelCollectorURL = "otel-collector:4317"
	subjectAnnounce  = "auction.announce"
	subjectBid       = "auction.bid"
	subjectResult    = "auction.result"
	auctionDuration  = 10 * time.Second // время приёма ставок
)

type Auction struct {
	ID        string
	Announce  models.AuctionAnnounce
	Bids      []models.AuctionBid
	StartTime time.Time
	EndTime   time.Time
	Winner    *models.AuctionBid
}

type Auctioneer struct {
	auctions map[string]*Auction
	logger   *logrus.Logger
}

func main() {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	logger.Infof("Starting %s service", serviceName)

	// Инициализация OpenTelemetry
	shutdown, err := common.InitTracer(serviceName, otelCollectorURL)
	if err != nil {
		logger.Warnf("Failed to initialize OpenTelemetry: %v", err)
	} else {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdown(ctx); err != nil {
				logger.Errorf("Failed to shutdown tracer: %v", err)
			}
		}()
	}

	// Подключение к NATS
	client, err := natsclient.NewClient(natsURL)
	if err != nil {
		logger.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer client.Close()

	// Создаем stream для JetStream (опционально)
	client.CreateStream("AUCTION", []string{"auction.>"})

	auctioneer := &Auctioneer{
		auctions: make(map[string]*Auction),
		logger:   logger,
	}

	// Подписка на объявления аукционов (от оркестратора)
	subAnnounce, err := client.Subscribe(subjectAnnounce, func(msg *natsio.Msg) {
		ctx, span := common.StartSpan(context.Background(), "auctioneer.handle-announce")
		defer span.End()

		logger.Infof("Received auction announcement: %s", string(msg.Data))

		var announce models.AuctionAnnounce
		if err := json.Unmarshal(msg.Data, &announce); err != nil {
			logger.Errorf("Failed to unmarshal announcement: %v", err)
			span.RecordError(err)
			return
		}

		auctioneer.startAuction(ctx, announce, client)
	})
	if err != nil {
		logger.Fatalf("Failed to subscribe to %s: %v", subjectAnnounce, err)
	}
	defer subAnnounce.Unsubscribe()

	// Подписка на ставки от агентов
	subBid, err := client.Subscribe(subjectBid, func(msg *natsio.Msg) {
		ctx, span := common.StartSpan(context.Background(), "auctioneer.handle-bid")
		defer span.End()

		logger.Debugf("Received bid: %s", string(msg.Data))

		var bid models.AuctionBid
		if err := json.Unmarshal(msg.Data, &bid); err != nil {
			logger.Errorf("Failed to unmarshal bid: %v", err)
			span.RecordError(err)
			return
		}

		auctioneer.processBid(ctx, bid)
	})
	if err != nil {
		logger.Fatalf("Failed to subscribe to %s: %v", subjectBid, err)
	}
	defer subBid.Unsubscribe()

	logger.Infof("Service %s is listening on subjects %s, %s", serviceName, subjectAnnounce, subjectBid)

	// Ожидание сигнала завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan
	logger.Info("Shutting down...")
}

func (a *Auctioneer) startAuction(ctx context.Context, announce models.AuctionAnnounce, client *natsclient.Client) {
	_, span := common.StartSpan(ctx, "auctioneer.start-auction")
	defer span.End()

	auctionID := announce.TaskID
	auction := &Auction{
		ID:        auctionID,
		Announce:  announce,
		Bids:      []models.AuctionBid{},
		StartTime: time.Now(),
		EndTime:   time.Now().Add(auctionDuration),
	}
	a.auctions[auctionID] = auction

	span.SetAttributes(
		attribute.String("auction.id", auctionID),
		attribute.String("task.id", announce.TaskID),
		attribute.Int64("duration_sec", int64(auctionDuration.Seconds())),
	)

	a.logger.Infof("Auction %s started, accepting bids until %v", auctionID, auction.EndTime)

	// Запускаем таймер для завершения аукциона
	go a.waitForAuctionEnd(auctionID, client)
}

func (a *Auctioneer) processBid(ctx context.Context, bid models.AuctionBid) {
	_, span := common.StartSpan(ctx, "auctioneer.process-bid")
	defer span.End()

	auction, exists := a.auctions[bid.AuctionID]
	if !exists {
		a.logger.Warnf("Auction %s not found for bid from agent %s", bid.AuctionID, bid.AgentID)
		return
	}

	if time.Now().After(auction.EndTime) {
		a.logger.Warnf("Bid from agent %s arrived after auction deadline", bid.AgentID)
		return
	}

	// Проверяем минимальный score
	score := bid.CalculateScore()
	if score < auction.Announce.MinScore {
		a.logger.Debugf("Bid from agent %s rejected due to low score %.2f", bid.AgentID, score)
		return
	}

	auction.Bids = append(auction.Bids, bid)
	a.logger.Infof("Bid accepted from agent %s (score %.2f, cost %.2f)", bid.AgentID, score, bid.Cost)

	span.SetAttributes(
		attribute.String("agent.id", bid.AgentID),
		attribute.Float64("bid.score", score),
		attribute.Float64("bid.cost", bid.Cost),
	)
}

func (a *Auctioneer) waitForAuctionEnd(auctionID string, client *natsclient.Client) {
	auction := a.auctions[auctionID]
	time.Sleep(time.Until(auction.EndTime))

	_, span := common.StartSpan(context.Background(), "auctioneer.finish-auction")
	defer span.End()

	span.SetAttributes(attribute.String("auction.id", auctionID))

	// Определяем победителя
	var winner *models.AuctionBid
	var highestScore float64 = -1

	for i, bid := range auction.Bids {
		score := bid.CalculateScore()
		if score > highestScore {
			highestScore = score
			winner = &auction.Bids[i]
		}
	}

	if winner == nil {
		a.logger.Warnf("Auction %s has no valid bids", auctionID)
		return
	}

	auction.Winner = winner
	a.logger.Infof("Auction %s winner: agent %s with score %.2f", auctionID, winner.AgentID, highestScore)

	// Отправляем результат
	result := models.AuctionResult{
		BaseMessage: natsclient.CreateBaseMessage(
			models.MessageTypeAuctionResult,
			serviceName,
			winner.Source,
		),
		AuctionID:     auctionID,
		WinnerAgentID: winner.AgentID,
		WinnerBid:     *winner,
		TotalBids:     len(auction.Bids),
		DecisionTime:  time.Now(),
	}

	data, err := json.Marshal(result)
	if err != nil {
		a.logger.Errorf("Failed to marshal result: %v", err)
		return
	}

	if err := client.Conn.Publish(subjectResult, data); err != nil {
		a.logger.Errorf("Failed to publish result: %v", err)
	} else {
		a.logger.Infof("Auction result published to %s", subjectResult)
	}

	// Удаляем аукцион из памяти (можно сохранить в БД)
	delete(a.auctions, auctionID)
}
