package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
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
	session         *discordgo.Session
	aiservice       *ai.Service
	repo            *database.Repository
	events          *database.EventRepository
	scheduler       gocron.Scheduler
	emojiManager    *emoji.Manager
	horoscopeCfg    config.HoroscopeConfig
	reengageService *reengage.Service
	reengageCfg     config.ReengageConfig
}

func New(session *discordgo.Session, aiService *ai.Service, repo *database.Repository, emojiManager *emoji.Manager, horoscopeCfg config.HoroscopeConfig, memoryService memory.Service, reengageCfg config.ReengageConfig, defaultPrompt string) *Scheduler {
	location, _ := time.LoadLocation("Europe/Paris")
	schedulerLogger := gocron.NewLogger(gocron.LogLevelInfo)
	scheduler, schedulerErr := gocron.NewScheduler(gocron.WithLocation(location), gocron.WithLogger(schedulerLogger))
	if schedulerErr != nil {
		log.Println("error creating scheduler,", schedulerErr)
	}

	reengageService := reengage.NewService(session, aiService, repo, memoryService, emojiManager, reengageCfg, defaultPrompt)

	return &Scheduler{
		session:         session,
		aiservice:       aiService,
		repo:            repo,
		events:          database.NewEventRepository(utils.GetDB(), logger.Log),
		scheduler:       scheduler,
		emojiManager:    emojiManager,
		horoscopeCfg:    horoscopeCfg,
		reengageService: reengageService,
		reengageCfg:     reengageCfg,
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
		log.Println("error creating farting job,", err)
	}

	_, err = s.scheduler.NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(3, 0, 0))),
		gocron.NewTask(
			s.CleanupOldEvents,
		),
	)
	if err != nil {
		log.Println("error creating cleanup job,", err)
	}

	if s.reengageCfg.CheckIntervalSeconds > 0 {
		_, err = s.scheduler.NewJob(
			gocron.DurationJob(time.Duration(s.reengageCfg.CheckIntervalSeconds)*time.Second),
			gocron.NewTask(s.CheckReengage),
		)
		if err != nil {
			log.Println("error creating reengage job,", err)
		}
	}

	s.scheduler.Start()
	log.Println("Scheduler started")
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

func (s *Scheduler) CheckReengage() {
	ctx := context.Background()
	if err := s.reengageService.CheckAllChannels(ctx); err != nil {
		logger.Error("Failed to check reengage channels", zap.Error(err))
	}
}
