package ice

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andrew-k681/go-chat/server/internal/config"
)

func TestCredentialKnownVector(t *testing.T) {
	// echo -n "1700000000:alice" | openssl dgst -sha1 -hmac "secret" -binary | base64
	if got := Credential("secret", "1700000000:alice"); got != "d8soP47RbdIKLDUOpnJPVQyq5Ts=" {
		t.Errorf("Credential = %s", got)
	}
}

func TestServers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	p := NewProvider(config.Config{
		STUNURLs:   []string{"stun:s:3478"},
		TURNURLs:   []string{"turn:t:3478", "turns:t:5349"},
		TURNSecret: config.Secret(strings.Repeat("k", 32)),
		TURNTTL:    time.Hour,
	})
	p.now = func() time.Time { return now }

	got := p.Servers()
	if len(got) != 2 || got[0].Username != "" || len(got[1].URLs) != 2 {
		t.Fatalf("Servers = %+v", got)
	}
	expiry, _, ok := strings.Cut(got[1].Username, ":")
	if !ok || expiry != strconv.FormatInt(now.Add(time.Hour).Unix(), 10) {
		t.Errorf("username %q should start with expiry", got[1].Username)
	}
	if got[1].Credential != Credential(p.secret, got[1].Username) {
		t.Error("credential does not match username")
	}
	if p.Servers()[1].Username == got[1].Username {
		t.Error("usernames should be unique per call")
	}
}

func TestSTUNOnly(t *testing.T) {
	got := NewProvider(config.Config{STUNURLs: []string{"stun:s"}}).Servers()
	if len(got) != 1 || got[0].Credential != "" {
		t.Errorf("Servers = %+v", got)
	}
}
