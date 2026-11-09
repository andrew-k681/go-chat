// Package ice builds the RTCIceServer list handed to clients, including
// short-lived TURN credentials in coturn's "use-auth-secret" (TURN REST API) format.
package ice

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"strconv"
	"time"

	"github.com/andrew-k681/go-chat/server/internal/config"
)

// Server mirrors the browser's RTCIceServer dictionary.
type Server struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type Provider struct {
	stun   []string
	turn   []string
	secret config.Secret
	ttl    time.Duration
	now    func() time.Time
}

func NewProvider(cfg config.Config) *Provider {
	return &Provider{
		stun:   cfg.STUNURLs,
		turn:   cfg.TURNURLs,
		secret: cfg.TURNSecret,
		ttl:    cfg.TURNTTL,
		now:    time.Now,
	}
}

// Servers returns the ICE servers for one client. TURN credentials are fresh
// on every call and expire after the configured TTL.
func (p *Provider) Servers() []Server {
	var out []Server
	if len(p.stun) > 0 {
		out = append(out, Server{URLs: p.stun})
	}
	if len(p.turn) > 0 {
		user := strconv.FormatInt(p.now().Add(p.ttl).Unix(), 10) + ":" + rand.Text()
		out = append(out, Server{URLs: p.turn, Username: user, Credential: Credential(p.secret, user)})
	}
	return out
}

// Credential is base64(HMAC-SHA1(secret, username)), as coturn expects.
func Credential(secret config.Secret, username string) string {
	mac := hmac.New(sha1.New, []byte(secret.Reveal()))
	mac.Write([]byte(username))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
