package scheduler

import (
	"context"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/go-co-op/gocron/v2"
	"go.uber.org/zap"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/config"
	"polynux/disgoroq/database"
	"polynux/disgoroq/emoji"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/memory"
	"polynux/disgoroq/reengage"
	"polynux/disgoroq/utils"
)

type Scheduler struct {
	client          *bot.Client
	aiservice       *ai.Service
	repo            *database.Repository
	events          *database.EventRepository
	scheduler       gocron.Scheduler
	emojiManager    *emoji.Manager
	horoscopeCfg    config.HoroscopeConfig
	reengageService *reengage.Service
	reengageCfg     config.ReengageConfig
	memoryCfg       config.MemoryConfig
}

func New(client *bot.Client, aiService *ai.Service, repo *database.Repository, emojiManager *emoji.Manager, horoscopeCfg config.HoroscopeConfig, memoryService memory.Service, reengageCfg config.ReengageConfig, defaultPrompt string, memoryCfg config.MemoryConfig) *Scheduler {
	location, _ := time.LoadLocation("Europe/Paris")
	schedulerLogger := gocron.NewLogger(gocron.LogLevelInfo)
	scheduler, schedulerErr := gocron.NewScheduler(gocron.WithLocation(location), gocron.WithLogger(schedulerLogger))
	if schedulerErr != nil {
		logger.Error("Failed to create scheduler", zap.Error(schedulerErr))
	}

	reengageService := reengage.NewService(client, aiService, repo, memoryService, emojiManager, reengageCfg, defaultPrompt)

	return &Scheduler{
		client:          client,
		aiservice:       aiService,
		repo:            repo,
		events:          database.NewEventRepository(utils.GetDB(), logger.Log),
		scheduler:       scheduler,
		emojiManager:    emojiManager,
		horoscopeCfg:    horoscopeCfg,
		reengageService: reengageService,
		reengageCfg:     reengageCfg,
		memoryCfg:       memoryCfg,
	}
}

func (s *Scheduler) Start() {
	_, err := s.scheduler.NewJob(
		gocron.WeeklyJob(1, gocron.NewWeekdays(time.Friday), gocron.NewAtTimes(gocron.NewAtTime(0, 0, 0))),
		gocron.NewTask(
			s.SendFartingFriday,
		),
	)
	if err != nil {
		logger.Error("Failed to create farting friday job", zap.Error(err))
	}

	_, err = s.scheduler.NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(3, 0, 0))),
		gocron.NewTask(
			s.CleanupOldEvents,
		),
	)
	if err != nil {
		logger.Error("Failed to create cleanup job", zap.Error(err))
	}

	if s.reengageCfg.CheckIntervalSeconds > 0 {
		_, err = s.scheduler.NewJob(
			gocron.DurationJob(time.Duration(s.reengageCfg.CheckIntervalSeconds)*time.Second),
			gocron.NewTask(s.CheckReengage),
		)
		if err != nil {
			logger.Error("Failed to create reengage job", zap.Error(err))
		}
	}

	if s.memoryCfg.RetentionDays > 0 {
		_, err = s.scheduler.NewJob(
			gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(3, 30, 0))),
			gocron.NewTask(s.CleanupMemoryRetention),
		)
		if err != nil {
			logger.Error("Failed to create memory retention job", zap.Error(err))
		}
	}

	s.scheduler.Start()
	logger.Info("Scheduler started")
}

func (s *Scheduler) Shutdown() error {
	return s.scheduler.Shutdown()
}

func (s *Scheduler) CleanupOldEvents() {
	ctx := context.Background()
	retentionDays := logger.GetRetentionDays()

	logger.Info("Starting event cleanup",
		zap.Int("retention_days", retentionDays),
	)

	if err := s.events.DeleteOldEvents(ctx, retentionDays); err != nil {
		logger.Error("Failed to cleanup old events", zap.Error(err))
	} else {
		logger.Info("Event cleanup completed successfully",
			zap.Int("retention_days", retentionDays),
		)
	}
}

// CleanupMemoryRetention deletes buffered messages, cached Discord messages,
// and attachment cache entries older than the configured retention period.
// Conversation summaries are intentionally never deleted.
func (s *Scheduler) CleanupMemoryRetention() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cutoff := time.Now().AddDate(0, 0, -s.memoryCfg.RetentionDays).Unix()

	logger.Info("Starting memory retention cleanup",
		zap.Int("retention_days", s.memoryCfg.RetentionDays),
	)

	bufferDeleted, err := s.repo.DeleteMessageBufferOlderThan(ctx, cutoff)
	if err != nil {
		logger.Error("Failed to cleanup old message buffer entries",
			zap.Error(err),
			zap.Int("retention_days", s.memoryCfg.RetentionDays))
	} else {
		logger.Info("Memory buffer retention cleanup done",
			zap.Int64("rows_deleted", bufferDeleted),
			zap.Int("retention_days", s.memoryCfg.RetentionDays))
	}

	messagesDeleted, err := s.repo.DeleteDiscordMessagesOlderThan(ctx, cutoff)
	if err != nil {
		logger.Error("Failed to cleanup old Discord messages",
			zap.Error(err),
			zap.Int("retention_days", s.memoryCfg.RetentionDays))
	} else {
		logger.Info("Discord messages retention cleanup done",
			zap.Int64("rows_deleted", messagesDeleted),
			zap.Int("retention_days", s.memoryCfg.RetentionDays))
	}

	cacheDeleted, err := s.repo.DeleteAttachmentCacheOlderThan(ctx, cutoff)
	if err != nil {
		logger.Error("Failed to cleanup old attachment cache entries",
			zap.Error(err),
			zap.Int("retention_days", s.memoryCfg.RetentionDays))
	} else {
		logger.Info("Attachment cache retention cleanup done",
			zap.Int64("rows_deleted", cacheDeleted),
			zap.Int("retention_days", s.memoryCfg.RetentionDays))
	}
}

func (s *Scheduler) CheckReengage() {
	ctx := context.Background()
	if err := s.reengageService.CheckAllChannels(ctx); err != nil {
		logger.Error("Failed to check reengage channels", zap.Error(err))
	}
}
