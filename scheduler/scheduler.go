package scheduler

import (
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/go-co-op/gocron/v2"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/database"
)

type Scheduler struct {
	session   *discordgo.Session
	provider  ai.Provider
	repo      *database.Repository
	scheduler gocron.Scheduler
}

func New(session *discordgo.Session, provider ai.Provider, repo *database.Repository) *Scheduler {
	location, _ := time.LoadLocation("Europe/Paris")
	logger := gocron.NewLogger(gocron.LogLevelInfo)
	scheduler, schedulerErr := gocron.NewScheduler(gocron.WithLocation(location), gocron.WithLogger(logger))
	if schedulerErr != nil {
		log.Println("error creating scheduler,", schedulerErr)
	}

	return &Scheduler{
		session:   session,
		provider:  provider,
		repo:      repo,
		scheduler: scheduler,
	}
}

func (s *Scheduler) Start() {
	_, jobErr := s.scheduler.NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(12, 0, 0))),
		gocron.NewTask(
			s.SendHoroscope,
		),
	)
	if jobErr != nil {
		log.Println("error creating horoscope job,", jobErr)
	}

	_, err := s.scheduler.NewJob(
		gocron.WeeklyJob(1, gocron.NewWeekdays(time.Friday), gocron.NewAtTimes(gocron.NewAtTime(0, 0, 0))),
		gocron.NewTask(
			s.SendFartingFriday,
		),
	)
	if err != nil {
		log.Println("error creating farting job,", err)
	}

	s.scheduler.Start()
	log.Println("Scheduler started")
}

func (s *Scheduler) Shutdown() error {
	return s.scheduler.Shutdown()
}
