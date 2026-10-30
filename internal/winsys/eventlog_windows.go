//go:build windows

package winsys

import (
	"golang.org/x/sys/windows/svc/eventlog"
)

// Event IDs, so administrators can filter for them.
const (
	EventStarted    = 1
	EventStopped    = 2
	EventCorrected  = 10
	EventBigJump    = 11
	EventFailing    = 20
	EventRecovered  = 21
	EventConfigBad  = 30
	EventMonitoring = 40
)

// InstallEventSource registers TimeSync as a source in the Application log.
func InstallEventSource(name string) error {
	err := eventlog.InstallAsEventCreate(name, eventlog.Error|eventlog.Warning|eventlog.Info)
	if err != nil && isAlreadyExists(err) {
		return nil
	}
	return err
}

// RemoveEventSource undoes InstallEventSource.
func RemoveEventSource(name string) error {
	return eventlog.Remove(name)
}

// EventLog writes to the Application log. A nil *EventLog does nothing.
type EventLog struct{ l *eventlog.Log }

// OpenEventLog returns nil if the source is not registered.
func OpenEventLog(name string) *EventLog {
	l, err := eventlog.Open(name)
	if err != nil {
		return nil
	}
	return &EventLog{l: l}
}

func (e *EventLog) Info(id uint32, msg string) {
	if e != nil {
		_ = e.l.Info(id, msg)
	}
}

func (e *EventLog) Warning(id uint32, msg string) {
	if e != nil {
		_ = e.l.Warning(id, msg)
	}
}

func (e *EventLog) Error(id uint32, msg string) {
	if e != nil {
		_ = e.l.Error(id, msg)
	}
}

func (e *EventLog) Close() {
	if e != nil {
		e.l.Close()
	}
}
