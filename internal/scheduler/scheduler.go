// Package scheduler provides cron job functionality with natural language support.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Job represents a scheduled task
type Job struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Schedule    string            `json:"schedule"`      // Natural language schedule
	Cron        string            `json:"cron"`          // Parsed cron expression
	Agent       string            `json:"agent"`         // Agent to run the task
	Task        string            `json:"task"`          // Task/prompt for the agent
	Cluster     string            `json:"cluster"`
	Namespace   string            `json:"namespace"`
	Enabled     bool              `json:"enabled"`
	CreatedAt   time.Time         `json:"created_at"`
	LastRun     *time.Time        `json:"last_run,omitempty"`
	NextRun     *time.Time        `json:"next_run,omitempty"`
	RunCount    int               `json:"run_count"`
	LastResult  string            `json:"last_result,omitempty"`
	LastError   string            `json:"last_error,omitempty"`
	Config      map[string]string `json:"config,omitempty"`
}

// JobRun represents a single execution of a job
type JobRun struct {
	ID        string    `json:"id"`
	JobID     string    `json:"job_id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	Status    string    `json:"status"` // "running", "success", "failed"
	Output    string    `json:"output"`
	Error     string    `json:"error,omitempty"`
}

// Scheduler manages cron jobs
type Scheduler struct {
	dataDir   string
	jobs      map[string]*Job
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	running   bool
	jobRunner JobRunner
}

// JobRunner is called when a job needs to run
type JobRunner func(ctx context.Context, job *Job) (string, error)

// NewScheduler creates a new scheduler
func NewScheduler(dataDir string) *Scheduler {
	return &Scheduler{
		dataDir: dataDir,
		jobs:    make(map[string]*Job),
	}
}

// SetJobRunner sets the function to run jobs
func (s *Scheduler) SetJobRunner(runner JobRunner) {
	s.jobRunner = runner
}

// Load loads jobs from disk
func (s *Scheduler) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobsFile := filepath.Join(s.dataDir, "jobs.json")
	data, err := os.ReadFile(jobsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var jobs []*Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return err
	}

	for _, job := range jobs {
		s.jobs[job.ID] = job
	}

	return nil
}

// Save saves jobs to disk
func (s *Scheduler) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := os.MkdirAll(s.dataDir, 0755); err != nil {
		return err
	}

	var jobs []*Job
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}

	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(s.dataDir, "jobs.json"), data, 0644)
}

// CreateJob creates a new scheduled job
func (s *Scheduler) CreateJob(name, schedule, agent, task, cluster, namespace string) (*Job, error) {
	// Parse natural language schedule to cron
	cron, err := ParseSchedule(schedule)
	if err != nil {
		return nil, fmt.Errorf("invalid schedule: %w", err)
	}

	job := &Job{
		ID:          uuid.New().String()[:8],
		Name:        name,
		Schedule:    schedule,
		Cron:        cron,
		Agent:       agent,
		Task:        task,
		Cluster:     cluster,
		Namespace:   namespace,
		Enabled:     true,
		CreatedAt:   time.Now(),
	}

	// Calculate next run
	nextRun := NextRunTime(cron)
	job.NextRun = &nextRun

	s.mu.Lock()
	s.jobs[job.ID] = job
	s.mu.Unlock()

	if err := s.Save(); err != nil {
		return nil, err
	}

	return job, nil
}

// GetJob returns a job by ID
func (s *Scheduler) GetJob(id string) (*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	job, ok := s.jobs[id]
	if !ok {
		return nil, fmt.Errorf("job not found: %s", id)
	}
	return job, nil
}

// ListJobs returns all jobs for a namespace
func (s *Scheduler) ListJobs(cluster, namespace string) []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var jobs []*Job
	for _, job := range s.jobs {
		if job.Cluster == cluster && job.Namespace == namespace {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

// DeleteJob deletes a job
func (s *Scheduler) DeleteJob(id string) error {
	s.mu.Lock()
	delete(s.jobs, id)
	s.mu.Unlock()

	return s.Save()
}

// EnableJob enables a job
func (s *Scheduler) EnableJob(id string) error {
	s.mu.Lock()
	if job, ok := s.jobs[id]; ok {
		job.Enabled = true
		nextRun := NextRunTime(job.Cron)
		job.NextRun = &nextRun
	}
	s.mu.Unlock()

	return s.Save()
}

// DisableJob disables a job
func (s *Scheduler) DisableJob(id string) error {
	s.mu.Lock()
	if job, ok := s.jobs[id]; ok {
		job.Enabled = false
		job.NextRun = nil
	}
	s.mu.Unlock()

	return s.Save()
}

// Start starts the scheduler loop
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.mu.Unlock()

	go s.run()
	return nil
}

// Stop stops the scheduler
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running && s.cancel != nil {
		s.cancel()
		s.running = false
	}
}

// run is the main scheduler loop
func (s *Scheduler) run() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.checkJobs()
		}
	}
}

// checkJobs checks if any jobs need to run
func (s *Scheduler) checkJobs() {
	s.mu.RLock()
	jobsToRun := make([]*Job, 0)
	now := time.Now()

	for _, job := range s.jobs {
		if !job.Enabled || job.NextRun == nil {
			continue
		}
		if now.After(*job.NextRun) || now.Equal(*job.NextRun) {
			jobsToRun = append(jobsToRun, job)
		}
	}
	s.mu.RUnlock()

	// Run jobs
	for _, job := range jobsToRun {
		go s.runJob(job)
	}
}

// runJob executes a job
func (s *Scheduler) runJob(job *Job) {
	if s.jobRunner == nil {
		return
	}

	// Save previous LastRun before updating (for the job runner to use)
	s.mu.Lock()
	var previousRun *time.Time
	if job.LastRun != nil {
		t := *job.LastRun
		previousRun = &t
	}
	now := time.Now()
	job.LastRun = &now
	job.RunCount++
	nextRun := NextRunTime(job.Cron)
	job.NextRun = &nextRun
	// Store previous run in Config for job runner to access
	if job.Config == nil {
		job.Config = make(map[string]string)
	}
	if previousRun != nil {
		job.Config["_previousRun"] = fmt.Sprintf("%d", previousRun.Unix())
	} else {
		job.Config["_previousRun"] = fmt.Sprintf("%d", now.Add(-5*time.Minute).Unix())
	}
	s.mu.Unlock()

	// Run the job
	result, err := s.jobRunner(s.ctx, job)

	// Update result
	s.mu.Lock()
	if err != nil {
		job.LastError = err.Error()
		job.LastResult = ""
	} else {
		job.LastResult = result
		job.LastError = ""
	}
	s.mu.Unlock()

	s.Save()
}

// RunJobNow runs a job immediately
func (s *Scheduler) RunJobNow(id string) error {
	s.mu.RLock()
	job, ok := s.jobs[id]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}

	go s.runJob(job)
	return nil
}

// ParseSchedule converts natural language to cron expression
func ParseSchedule(input string) (string, error) {
	input = strings.ToLower(strings.TrimSpace(input))

	// Direct cron expression (5 or 6 fields)
	if isCronExpression(input) {
		return input, nil
	}

	// Common patterns
	patterns := map[string]string{
		// Every X minutes/hours
		"every minute":     "* * * * *",
		"every 5 minutes":  "*/5 * * * *",
		"every 10 minutes": "*/10 * * * *",
		"every 15 minutes": "*/15 * * * *",
		"every 30 minutes": "*/30 * * * *",
		"every hour":       "0 * * * *",
		"hourly":           "0 * * * *",
		"every 2 hours":    "0 */2 * * *",
		"every 3 hours":    "0 */3 * * *",
		"every 4 hours":    "0 */4 * * *",
		"every 6 hours":    "0 */6 * * *",
		"every 12 hours":   "0 */12 * * *",

		// Daily
		"every day":          "0 9 * * *",
		"daily":              "0 9 * * *",
		"every morning":      "0 9 * * *",
		"every evening":      "0 18 * * *",
		"every night":        "0 21 * * *",
		"every day at noon":  "0 12 * * *",
		"every day at midnight": "0 0 * * *",

		// Weekly
		"every week":    "0 9 * * 1",
		"weekly":        "0 9 * * 1",
		"every monday":  "0 9 * * 1",
		"every tuesday": "0 9 * * 2",
		"every wednesday": "0 9 * * 3",
		"every thursday":  "0 9 * * 4",
		"every friday":    "0 9 * * 5",
		"every saturday":  "0 9 * * 6",
		"every sunday":    "0 9 * * 0",
		"every weekday":   "0 9 * * 1-5",
		"every weekend":   "0 10 * * 0,6",

		// Monthly
		"every month":           "0 9 1 * *",
		"monthly":               "0 9 1 * *",
		"first of every month":  "0 9 1 * *",
		"last day of month":     "0 9 L * *",
	}

	// Check exact matches
	if cron, ok := patterns[input]; ok {
		return cron, nil
	}

	// Parse "every X minutes"
	if match := regexp.MustCompile(`every (\d+) minutes?`).FindStringSubmatch(input); match != nil {
		mins, _ := strconv.Atoi(match[1])
		if mins > 0 && mins < 60 {
			return fmt.Sprintf("*/%d * * * *", mins), nil
		}
	}

	// Parse "every X hours"
	if match := regexp.MustCompile(`every (\d+) hours?`).FindStringSubmatch(input); match != nil {
		hours, _ := strconv.Atoi(match[1])
		if hours > 0 && hours < 24 {
			return fmt.Sprintf("0 */%d * * *", hours), nil
		}
	}

	// Parse "every day at X" or "daily at X"
	if match := regexp.MustCompile(`(?:every day|daily) at (\d{1,2})(?::(\d{2}))?\s*(am|pm)?`).FindStringSubmatch(input); match != nil {
		hour, _ := strconv.Atoi(match[1])
		minute := 0
		if match[2] != "" {
			minute, _ = strconv.Atoi(match[2])
		}
		if match[3] == "pm" && hour < 12 {
			hour += 12
		} else if match[3] == "am" && hour == 12 {
			hour = 0
		}
		return fmt.Sprintf("%d %d * * *", minute, hour), nil
	}

	// Parse "at X:XX" (assumes daily)
	if match := regexp.MustCompile(`at (\d{1,2}):(\d{2})\s*(am|pm)?`).FindStringSubmatch(input); match != nil {
		hour, _ := strconv.Atoi(match[1])
		minute, _ := strconv.Atoi(match[2])
		if match[3] == "pm" && hour < 12 {
			hour += 12
		} else if match[3] == "am" && hour == 12 {
			hour = 0
		}
		return fmt.Sprintf("%d %d * * *", minute, hour), nil
	}

	// Parse "every [weekday] at X"
	weekdays := map[string]string{
		"monday": "1", "tuesday": "2", "wednesday": "3",
		"thursday": "4", "friday": "5", "saturday": "6", "sunday": "0",
	}
	for day, num := range weekdays {
		pattern := fmt.Sprintf(`every %s at (\d{1,2})(?::(\d{2}))?\s*(am|pm)?`, day)
		if match := regexp.MustCompile(pattern).FindStringSubmatch(input); match != nil {
			hour, _ := strconv.Atoi(match[1])
			minute := 0
			if match[2] != "" {
				minute, _ = strconv.Atoi(match[2])
			}
			if match[3] == "pm" && hour < 12 {
				hour += 12
			} else if match[3] == "am" && hour == 12 {
				hour = 0
			}
			return fmt.Sprintf("%d %d * * %s", minute, hour, num), nil
		}
	}

	return "", fmt.Errorf("could not parse schedule: %s", input)
}

// isCronExpression checks if input looks like a cron expression
func isCronExpression(input string) bool {
	parts := strings.Fields(input)
	return len(parts) == 5 || len(parts) == 6
}

// NextRunTime calculates the next run time from a cron expression
func NextRunTime(cronExpr string) time.Time {
	// Simple implementation - for production use a proper cron parser
	now := time.Now()
	parts := strings.Fields(cronExpr)
	if len(parts) < 5 {
		return now.Add(time.Hour)
	}

	// Parse minute and hour for basic scheduling
	minute := 0
	hour := now.Hour()

	if parts[0] != "*" && !strings.HasPrefix(parts[0], "*/") {
		minute, _ = strconv.Atoi(parts[0])
	}
	if parts[1] != "*" && !strings.HasPrefix(parts[1], "*/") {
		hour, _ = strconv.Atoi(parts[1])
	}

	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())

	// If the time has passed today, move to next occurrence
	if next.Before(now) {
		if strings.HasPrefix(parts[0], "*/") {
			// Every X minutes
			interval, _ := strconv.Atoi(strings.TrimPrefix(parts[0], "*/"))
			next = now.Truncate(time.Duration(interval) * time.Minute).Add(time.Duration(interval) * time.Minute)
		} else if strings.HasPrefix(parts[1], "*/") {
			// Every X hours
			interval, _ := strconv.Atoi(strings.TrimPrefix(parts[1], "*/"))
			next = now.Truncate(time.Duration(interval) * time.Hour).Add(time.Duration(interval) * time.Hour)
		} else {
			// Daily or weekly - add a day
			next = next.Add(24 * time.Hour)
		}
	}

	return next
}

// FormatSchedule returns a human-readable schedule description
func FormatSchedule(cron string) string {
	parts := strings.Fields(cron)
	if len(parts) < 5 {
		return cron
	}

	minute, hour, dom, month, dow := parts[0], parts[1], parts[2], parts[3], parts[4]

	// Every X minutes
	if strings.HasPrefix(minute, "*/") && hour == "*" {
		interval := strings.TrimPrefix(minute, "*/")
		return fmt.Sprintf("Every %s minutes", interval)
	}

	// Every X hours
	if minute == "0" && strings.HasPrefix(hour, "*/") {
		interval := strings.TrimPrefix(hour, "*/")
		return fmt.Sprintf("Every %s hours", interval)
	}

	// Hourly
	if minute == "0" && hour == "*" {
		return "Every hour"
	}

	// Daily at specific time
	if dom == "*" && month == "*" && dow == "*" {
		h, _ := strconv.Atoi(hour)
		m, _ := strconv.Atoi(minute)
		return fmt.Sprintf("Daily at %02d:%02d", h, m)
	}

	// Weekly
	if dom == "*" && month == "*" && dow != "*" {
		h, _ := strconv.Atoi(hour)
		m, _ := strconv.Atoi(minute)
		dayName := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
		d, _ := strconv.Atoi(dow)
		if d >= 0 && d < 7 {
			return fmt.Sprintf("Every %s at %02d:%02d", dayName[d], h, m)
		}
	}

	return cron
}
