// Package scheduler handles scheduled job execution.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/log"
	"github.com/lajosnagyuk/prisn/pkg/runner"
	"github.com/lajosnagyuk/prisn/pkg/store"
)

// Scheduler runs scheduled jobs.
type Scheduler struct {
	store     *store.Store
	runner    *runner.Runner
	nodeID    string
	interval  time.Duration
	mu        sync.Mutex
	running   bool
	stopCh    chan struct{}
	wg        sync.WaitGroup
	maxWorkers int
	semaphore  chan struct{}
}

// Config holds scheduler configuration.
type Config struct {
	Store      *store.Store
	Runner     *runner.Runner
	NodeID     string
	Interval   time.Duration // How often to check for due jobs
	MaxWorkers int           // Max concurrent jobs
}

// NewScheduler creates a new scheduler.
func NewScheduler(cfg Config) *Scheduler {
	if cfg.Interval == 0 {
		cfg.Interval = 10 * time.Second
	}
	if cfg.MaxWorkers == 0 {
		cfg.MaxWorkers = 10
	}
	if cfg.NodeID == "" {
		cfg.NodeID = "scheduler-1"
	}

	return &Scheduler{
		store:      cfg.Store,
		runner:     cfg.Runner,
		nodeID:     cfg.NodeID,
		interval:   cfg.Interval,
		maxWorkers: cfg.MaxWorkers,
		semaphore:  make(chan struct{}, cfg.MaxWorkers),
		stopCh:     make(chan struct{}),
	}
}

// Start starts the scheduler.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("scheduler already running")
	}
	s.running = true
	s.mu.Unlock()

	log.Start("scheduler (%d workers, checking every %v)", s.maxWorkers, s.interval)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Run immediately on start
	s.checkAndRun(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.stopCh:
			log.Done("scheduler stopped")
			s.wg.Wait()
			return nil
		case <-ticker.C:
			s.checkAndRun(ctx)
		}
	}
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	close(s.stopCh)
	s.running = false
}

// checkAndRun checks for due jobs and runs them.
func (s *Scheduler) checkAndRun(ctx context.Context) {
	now := time.Now()

	schedules, err := s.store.GetDueCronSchedules(now)
	if err != nil {
		log.Fail("getting due schedules: %v", err)
		return
	}

	for _, sched := range schedules {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Acquire semaphore
		s.semaphore <- struct{}{}
		s.wg.Add(1)

		go func(sched *store.CronSchedule) {
			defer func() {
				<-s.semaphore
				s.wg.Done()
			}()

			s.runScheduledJob(ctx, sched)
		}(sched)
	}
}

// runScheduledJob runs a single scheduled job.
func (s *Scheduler) runScheduledJob(ctx context.Context, sched *store.CronSchedule) {
	// Get deployment config
	deploy, err := s.store.GetDeployment(sched.DeploymentID)
	if err != nil {
		log.Fail("cron %s: deployment gone", sched.DeploymentID)
		return
	}

	log.Start("cron %s", deploy.Name)

	// Create execution record
	exec := &store.Execution{
		ID:           store.NewExecutionID(),
		DeploymentID: sched.DeploymentID,
		Namespace:    deploy.Namespace,
		Name:         deploy.Name,
		Status:       "Running",
		StartedAt:    time.Now(),
		NodeID:       s.nodeID,
	}

	if err := s.store.CreateExecution(exec); err != nil {
		log.Fail("cron %s: can't record execution", deploy.Name)
		return
	}

	// Build run config
	cfg := runner.RunConfig{
		Script:  deploy.Source,
		Args:    deploy.Args,
		Env:     deploy.Env,
		Timeout: deploy.Timeout,
		Resources: runner.ResourceConfig{
			MaxMemoryMB:  int(deploy.Resources.MemoryBytes / (1024 * 1024)),
			MaxProcesses: deploy.Resources.MaxProcesses,
		},
	}

	// Run the job
	result, err := s.runner.Run(ctx, cfg)

	exec.CompletedAt = time.Now()
	exec.Duration = exec.CompletedAt.Sub(exec.StartedAt)

	if err != nil {
		exec.Status = "Failed"
		exec.Error = err.Error()
	} else {
		exec.ExitCode = result.ExitCode
		exec.Stdout = result.Stdout
		exec.Stderr = result.Stderr
		if result.ExitCode == 0 {
			exec.Status = "Completed"
		} else {
			exec.Status = "Failed"
		}
	}

	if err := s.store.UpdateExecution(exec); err != nil {
		log.Warn("cron %s: couldn't save result", deploy.Name)
	}

	// Calculate and update next run time
	cronExpr, err := ParseCron(ExpandShortcut(sched.Schedule))
	if err != nil {
		log.Warn("cron %s: bad schedule, disabling", deploy.Name)
		// Disable the schedule to prevent repeated failures
		s.store.DisableCronSchedule(sched.ID)
		return
	}

	nextRun := cronExpr.NextAfter(time.Now())
	if err := s.store.UpdateCronSchedule(sched.ID, nextRun, exec.CompletedAt); err != nil {
		log.Warn("cron %s: couldn't schedule next run, forcing delay", deploy.Name)
		// On DB error, force a minimum 30-second delay to prevent thundering herd
		fallbackNext := time.Now().Add(30 * time.Second)
		s.store.UpdateCronSchedule(sched.ID, fallbackNext, exec.CompletedAt)
	}

	// Log result
	log.LogExec(log.ExecEvent{
		Name:     deploy.Name,
		Duration: exec.Duration,
		ExitCode: exec.ExitCode,
		Error:    err,
	})
}

// RegisterDeployment registers a cron deployment with the scheduler.
func (s *Scheduler) RegisterDeployment(deploy *store.Deployment) error {
	if deploy.Schedule == "" {
		return fmt.Errorf("deployment has no schedule")
	}

	// Parse and validate cron expression
	cronExpr, err := ParseCron(ExpandShortcut(deploy.Schedule))
	if err != nil {
		return fmt.Errorf("invalid cron expression: %w", err)
	}

	// Calculate next run time
	nextRun := cronExpr.NextAfter(time.Now())

	// Create cron schedule record
	sched := &store.CronSchedule{
		ID:           store.NewCronID(),
		DeploymentID: deploy.ID,
		Schedule:     deploy.Schedule,
		NextRun:      nextRun,
		Enabled:      true,
	}

	return s.store.CreateCronSchedule(sched)
}

// Status returns the current scheduler status.
type Status struct {
	Running     bool      `json:"running"`
	NodeID      string    `json:"node_id"`
	MaxWorkers  int       `json:"max_workers"`
	ActiveJobs  int       `json:"active_jobs"`
	CheckInterval string  `json:"check_interval"`
}

// Status returns the current scheduler status.
func (s *Scheduler) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()

	activeJobs := 0
	// Count active workers by checking semaphore
	select {
	case s.semaphore <- struct{}{}:
		<-s.semaphore
		activeJobs = s.maxWorkers - (cap(s.semaphore) - len(s.semaphore))
	default:
		activeJobs = s.maxWorkers
	}

	return Status{
		Running:       s.running,
		NodeID:        s.nodeID,
		MaxWorkers:    s.maxWorkers,
		ActiveJobs:    activeJobs,
		CheckInterval: s.interval.String(),
	}
}
