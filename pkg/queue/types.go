package queue

import (
	"time"
)

// JobStatus represents the status of a job in the queue
type JobStatus string

const (
	JobStatusQueued JobStatus = "queued"
	JobStatusPicked JobStatus = "picked"
)

// StatisticsStatus represents the final status of a job for statistics
type StatisticsStatus string

const (
	StatisticsStatusException  StatisticsStatus = "exception"
	StatisticsStatusSuccessful StatisticsStatus = "successful"
)

// Job represents a job in the queue
type Job struct {
	ID         int32     `json:"id" db:"id"`
	Priority   int32     `json:"priority" db:"priority"`
	Created    time.Time `json:"created" db:"created"`
	Updated    time.Time `json:"updated" db:"updated"`
	Status     JobStatus `json:"status" db:"status"`
	Entrypoint string    `json:"entrypoint" db:"entrypoint"`
	Payload    []byte    `json:"payload" db:"payload"`
}

// QueueStatistics represents queue size statistics
type QueueStatistics struct {
	Count      int64     `json:"count" db:"count"`
	Priority   int32     `json:"priority" db:"priority"`
	Entrypoint string    `json:"entrypoint" db:"entrypoint"`
	Status     JobStatus `json:"status" db:"status"`
}

// LogStatistics represents job execution statistics
type LogStatistics struct {
	ID          int32            `json:"id" db:"id"`
	Count       int64            `json:"count" db:"count"`
	Created     time.Time        `json:"created" db:"created"`
	TimeInQueue time.Duration    `json:"time_in_queue" db:"time_in_queue"`
	Entrypoint  string           `json:"entrypoint" db:"entrypoint"`
	Priority    int32            `json:"priority" db:"priority"`
	Status      StatisticsStatus `json:"status" db:"status"`
}

// Event represents a PostgreSQL notification event
type Event struct {
	Channel   string    `json:"channel"`
	Operation string    `json:"operation"`
	SentAt    time.Time `json:"sent_at"`
	Table     string    `json:"table"`
}
