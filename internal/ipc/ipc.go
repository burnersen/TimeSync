// Package ipc is the line between the window (or the command line tool) and
// the service: a named pipe that exists only on this computer.
//
// Anyone signed in may ask for the status or for an immediate
// synchronization. That is harmless: the service only ever uses the servers
// an administrator configured. Settings are not changed through the pipe at
// all; they live in a file only administrators can write, and "reload" just
// asks the service to read it again.
package ipc

import (
	"time"

	"github.com/burnersen/TimeSync/internal/history"
	"github.com/burnersen/TimeSync/internal/syncer"
)

// PipeName is the pipe's address.
const PipeName = `\\.\pipe\TimeSync`

// Commands.
const (
	CmdStatus = "status"
	CmdSync   = "sync"
	CmdReload = "reload"
)

// Request is one line sent to the service.
type Request struct {
	Cmd string `json:"cmd"`
}

// Response is the service's one-line answer.
type Response struct {
	OK     bool           `json:"ok"`
	Error  string         `json:"error,omitempty"`
	Status *Status        `json:"status,omitempty"`
	Result *syncer.Result `json:"result,omitempty"`
}

// Status is the service's view of things.
type Status struct {
	Version         string         `json:"version"`
	StartedAt       time.Time      `json:"startedAt"`
	IntervalMinutes int            `json:"intervalMinutes"`
	NextSync        time.Time      `json:"nextSync"`
	Syncing         bool           `json:"syncing"`
	SlewUntil       time.Time      `json:"slewUntil,omitempty"`
	MonitorOnly     bool           `json:"monitorOnly"`
	Domain          string         `json:"domain,omitempty"`
	W32Time         string         `json:"w32time"`
	ConfigError     string         `json:"configError,omitempty"`
	Last            *history.Entry `json:"last,omitempty"`
	Stats           history.Stats  `json:"stats"`
}

// Timeouts for clients. A sync waits for all servers (5 s each, in
// parallel) plus a possible HTTPS fallback.
const (
	DialTimeout = 2 * time.Second
	SyncTimeout = 30 * time.Second
)
