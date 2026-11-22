package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/mediaserver/audiobookshelf"
	"github.com/makearr/Solarr/internal/mediaserver/calibre"
	"github.com/makearr/Solarr/internal/mediaserver/jellyfin"
	"github.com/makearr/Solarr/internal/mediaserver/kavita"
	"github.com/makearr/Solarr/internal/mediaserver/navidrome"
	"github.com/makearr/Solarr/internal/mediaserver/stash"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// mediaServerTimeout covers a library scan the server answers only when it has started.
const mediaServerTimeout = 30 * time.Second

// mediaServerClients builds the client of a connection by its kind, on a bucket of its own per host; the guard admits the connection's host, a LAN one included.
func (p *process) mediaServerClients(kind, baseURL, username string, credential secrets.Secret) (mediaserver.Server, error) {
	doer := p.providerClient(providerMediaServerPrefix+arrHost(baseURL), connectionGuard(baseURL), httpx.ClientConfig{Timeout: mediaServerTimeout})
	secret := credential.Reveal()
	switch kind {
	case "jellyfin":
		return jellyfin.New(doer, baseURL, secret), nil
	case "navidrome":
		return navidrome.New(doer, baseURL, username, secret, randomToken), nil
	case "audiobookshelf":
		return audiobookshelf.New(doer, baseURL, secret), nil
	case "kavita":
		return kavita.New(doer, baseURL, secret), nil
	case "calibre":
		return calibre.New(doer, baseURL, username, secret, randomToken), nil
	case "stash":
		return stash.New(doer, baseURL, secret), nil
	}
	return nil, fmt.Errorf("media server: unknown kind %q", kind)
}

// randomToken is a salt or a client nonce; a failing random source is a broken host, not a request to answer.
func randomToken() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		panic(fmt.Sprintf("media server: no random bytes: %v", err))
	}
	return hex.EncodeToString(raw)
}
