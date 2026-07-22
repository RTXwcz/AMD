package main

import (
	"fmt"
	"os/exec"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

type processLineMsg struct {
	runID    uint64
	jobIndex int
	line     string
}

type processDoneMsg struct {
	runID    uint64
	jobIndex int
	err      error
}

func startProcessEvents(cmd *exec.Cmd, runID uint64, jobIndex int) (<-chan tea.Msg, tea.Cmd) {
	events := make(chan tea.Msg, 64)
	start := func() tea.Msg {
		go produceProcessEvents(cmd, runID, jobIndex, events)
		return <-events
	}
	return events, start
}

func waitProcessEvent(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-events
		if !ok {
			return processDoneMsg{err: fmt.Errorf("download event stream closed unexpectedly")}
		}
		return msg
	}
}

func produceProcessEvents(cmd *exec.Cmd, runID uint64, jobIndex int, events chan<- tea.Msg) {
	defer close(events)
	emit := func(line string) {
		if line != "" {
			events <- processLineMsg{runID: runID, jobIndex: jobIndex, line: line}
		}
	}
	stdout := &eventLineWriter{emit: emit}
	stderr := &eventLineWriter{emit: emit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		events <- processDoneMsg{runID: runID, jobIndex: jobIndex, err: err}
		return
	}
	waitErr := cmd.Wait()
	stdout.Flush()
	stderr.Flush()
	events <- processDoneMsg{runID: runID, jobIndex: jobIndex, err: waitErr}
}

type eventLineWriter struct {
	mu      sync.Mutex
	pending []byte
	emit    func(string)
}

func (w *eventLineWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, data...)
	for len(w.pending) > 0 {
		advance, token, _ := splitOutputLines(w.pending, false)
		if advance == 0 {
			break
		}
		if len(token) > 0 {
			w.emit(string(token))
		}
		w.pending = w.pending[advance:]
	}
	return len(data), nil
}

func (w *eventLineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 {
		w.emit(string(w.pending))
		w.pending = nil
	}
}

func splitOutputLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			advance = i + 1
			for advance < len(data) && (data[advance] == '\n' || data[advance] == '\r') {
				advance++
			}
			return advance, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
