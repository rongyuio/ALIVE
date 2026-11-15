package relay

import (
	"strings"
	"testing"
)

func TestRelayLogCollectsCompleteSanitizedLines(t *testing.T) {
	l := &relayLog{token: "local-secret"}
	for _, part := range []string{"[hls @ 123] ", "Failed https://cdn.invalid/", "stream?signature=secret", " /local-secret\nlast diagnostic"} {
		if n, err := l.Write([]byte(part)); n != len(part) || err != nil {
			t.Fatal(n, err)
		}
	}
	l.flush()
	got := l.summary()
	if !strings.Contains(got, "[hls @ 123] Failed <url> /<token>") || !strings.Contains(got, "last diagnostic") {
		t.Fatalf("missing complete diagnostic: %s", got)
	}
	for _, secret := range []string{"signature", "secret", "cdn.invalid"} {
		if strings.Contains(got, secret) {
			t.Fatal("日志泄漏敏感值")
		}
	}
}

func TestRelayLogBoundsWholeLines(t *testing.T) {
	l := &relayLog{}
	l.Write([]byte("https://cdn.invalid/" + strings.Repeat("a", relayLogLineLimit)))
	l.Write([]byte("signature=private\n"))
	for i := 0; i < 10; i++ {
		l.Write([]byte("safe diagnostic\n"))
	}
	l.flush()
	if len(l.tail) != 4 || strings.Contains(l.summary(), "private") || len(l.pending) != 0 {
		t.Fatal("日志未有界脱敏")
	}
}
