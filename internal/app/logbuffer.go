package app

import (
	"strings"
	"sync"
)

type LogBuffer struct {
	mu      sync.Mutex
	max     int
	lines   []string
	partial string
}

func NewLogBuffer(max int) *LogBuffer {
	if max <= 0 {
		max = 200
	}
	return &LogBuffer{max: max}
}

func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.partial += string(p)
	for {
		idx := strings.IndexByte(b.partial, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(b.partial[:idx], "\r")
		b.partial = b.partial[idx+1:]
		if line != "" {
			b.lines = append(b.lines, line)
		}
	}
	if len(b.lines) > b.max {
		b.lines = b.lines[len(b.lines)-b.max:]
	}
	return len(p), nil
}

func (b *LogBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	lines := append([]string(nil), b.lines...)
	if b.partial != "" {
		lines = append(lines, strings.TrimRight(b.partial, "\r"))
	}
	return lines
}
