package relay

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

// relayLog 先组完整行再脱敏，避免 stderr 分次 Write 把地址和签名拆开后泄漏。
// 每秒最多输出一行，另留四行脱敏尾部供失败诊断；超长行整行丢弃，不保留截断签名。
type relayLog struct {
	mu       sync.Mutex
	last     time.Time
	token    string
	pending  string
	dropping bool
	tail     []string
}

const relayLogLineLimit = 4096

func (l *relayLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	remaining := string(p)
	for len(remaining) > 0 {
		part, rest, complete := strings.Cut(remaining, "\n")
		if !l.dropping {
			if len(l.pending)+len(part) > relayLogLineLimit {
				l.pending = ""
				l.dropping = true
			} else {
				l.pending += part
			}
		}
		if !complete {
			break
		}
		if !l.dropping {
			l.record(l.pending)
		}
		l.pending = ""
		l.dropping = false
		remaining = rest
	}
	return len(p), nil
}

func (l *relayLog) record(line string) {
	msg := truncateUTF8(scrubText(l.token, strings.TrimSpace(line)), 400)
	if msg == "" {
		return
	}
	l.tail = append(l.tail, msg)
	if len(l.tail) > 4 {
		l.tail = l.tail[len(l.tail)-4:]
	}
	if time.Since(l.last) >= time.Second {
		l.last = time.Now()
		slog.Warn("ffmpeg", "message", msg)
	}
}

func (l *relayLog) flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.dropping {
		l.record(l.pending)
	}
	l.pending = ""
	l.dropping = false
}

func (l *relayLog) summary() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.tail, " | ")
}
