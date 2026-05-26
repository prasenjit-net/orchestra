package workflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/prasenjit-net/orchestra/internal/config"
	"github.com/prasenjit-net/orchestra/internal/livebus"
)

var ErrNotFound = errors.New("workflow resource not found")

type Service struct {
	db         *sql.DB
	dialect    Dialect
	logger     *slog.Logger
	cfg        config.WorkflowConfig
	activities map[string]Activity
	workerID   string
	live       *livebus.Bus
	wakeCh     chan struct{}
}

// rebind rewrites ? placeholders to $N for PostgreSQL; identity for SQLite.
func (s *Service) rebind(query string) string { return s.dialect.Rebind(query) }

// notifyWorker pings the worker goroutine to run a pass immediately.
// Non-blocking: if a ping is already pending the new one is dropped (one is enough).
func (s *Service) notifyWorker() {
	if s == nil {
		return
	}
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

func NewService(cfg config.WorkflowConfig, logger *slog.Logger, buses ...*livebus.Bus) (*Service, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	dialect := Dialect(cfg.DatabaseDriver)
	if dialect == "" {
		dialect = DialectSQLite
	}

	var (
		db  *sql.DB
		err error
	)

	switch dialect {
	case DialectPostgres:
		if cfg.DatabaseURL == "" {
			return nil, fmt.Errorf("workflow.databaseURL is required when databaseDriver is postgres")
		}
		db, err = sql.Open("pgx", cfg.DatabaseURL)
		if err != nil {
			return nil, fmt.Errorf("open postgres workflow database: %w", err)
		}
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(5)
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer pingCancel()
		if err = db.PingContext(pingCtx); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("ping postgres workflow database: %w", err)
		}
	default:
		dialect = DialectSQLite
		if err = ensureDatabasePath(cfg.DatabasePath); err != nil {
			return nil, err
		}
		db, err = sql.Open("sqlite", cfg.DatabasePath)
		if err != nil {
			return nil, fmt.Errorf("open workflow database: %w", err)
		}
		db.SetMaxOpenConns(1)
		for _, pragma := range []string{
			`PRAGMA journal_mode = WAL`,
			`PRAGMA busy_timeout = 5000`,
			`PRAGMA foreign_keys = ON`,
		} {
			if _, err := db.Exec(pragma); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("configure workflow database: %w", err)
			}
		}
	}

	live := livebus.New()
	if len(buses) > 0 && buses[0] != nil {
		live = buses[0]
	}

	svc := &Service{
		db:         db,
		dialect:    dialect,
		logger:     logger.With("component", "workflow"),
		cfg:        cfg,
		activities: make(map[string]Activity),
		workerID:   generateID("worker"),
		live:       live,
		wakeCh:     make(chan struct{}, 1),
	}

	for _, activity := range builtInActivities(cfg, svc.logger) {
		svc.activities[activity.Descriptor().Name] = activity
	}
	// Always register script activity with DB-backed lookup so saved scripts work
	// regardless of the scriptEnabled config flag.
	svc.activities["script"] = newScriptActivity(cfg, svc.lookupScriptSource)
	svc.activities["agent"] = newAgentActivity(cfg, svc.lookupAgent, svc.GetAgentMCPServers)

	// For PostgreSQL the schema must be created manually via `orchestra schema --create`.
	if !dialect.IsPostgres() {
		if err := svc.initSchema(context.Background()); err != nil {
			_ = db.Close()
			return nil, err
		}
	}

	return svc, nil
}

func (s *Service) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Service) SubscribeLiveEvents() (<-chan livebus.Event, func()) {
	if s == nil || s.live == nil {
		ch := make(chan livebus.Event)
		close(ch)
		return ch, func() {}
	}
	return s.live.Subscribe()
}

func (s *Service) emitLiveEvent(eventType, entity, entityID string, payload any) {
	if s == nil || s.live == nil {
		return
	}
	s.live.Publish(livebus.NewEvent(eventType, entity, entityID, payload))
}

func (s *Service) emitOperationEvent(workflowID, eventType string, payload any) {
	s.emitLiveEvent("operation.event", "operation", workflowID, map[string]any{
		"workflowId": workflowID,
		"eventType":  eventType,
		"payload":    payload,
	})
}

func (s *Service) Start(ctx context.Context) {
	if s == nil {
		return
	}

	go func() {
		ticker := time.NewTicker(s.cfg.PollInterval)
		defer ticker.Stop()

		s.runWorkerPass(ctx)

		for {
			select {
			case <-ctx.Done():
				return
			case <-s.wakeCh:
				s.runWorkerPass(ctx)
			case <-ticker.C:
				s.runWorkerPass(ctx)
			}
		}
	}()
}

func (s *Service) runWorkerPass(ctx context.Context) {
	if err := s.requeueExpiredTasks(ctx); err != nil {
		s.logger.Error("requeue expired tasks", "error", err)
	}
	for range 16 {
		processed, err := s.RunOnce(ctx)
		if err != nil {
			s.logger.Error("process workflow task", "error", err)
			return
		}
		if !processed {
			return
		}
	}
}

func (s *Service) ListActivities() []ActivityDescriptor {
	if s == nil {
		return nil
	}

	descriptors := make([]ActivityDescriptor, 0, len(s.activities))
	for _, activity := range s.activities {
		descriptors = append(descriptors, activity.Descriptor())
	}
	slices.SortFunc(descriptors, func(a, b ActivityDescriptor) int {
		return strings.Compare(a.Name, b.Name)
	})
	return descriptors
}
