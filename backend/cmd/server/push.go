package main

import (
	"log/slog"
	"os"
	"strings"

	"aiworkforce/backend/internal/infrastructure/postgres"
	"aiworkforce/backend/internal/push"
)

// wirePush enables Web Push only when VAPID_PUBLIC_KEY, VAPID_PRIVATE_KEY and
// VAPID_SUBJECT (mailto: or https: contact) are all set and valid. Optional
// PUSH_ALLOWED_HOSTS (comma separated) extends the push-service allow-list.
func wirePush(log *slog.Logger, pg *postgres.Store) *push.Service {
	pubKey, privKey, subject := os.Getenv("VAPID_PUBLIC_KEY"), os.Getenv("VAPID_PRIVATE_KEY"), os.Getenv("VAPID_SUBJECT")
	if pubKey == "" || privKey == "" || subject == "" {
		log.Info("web push disabled (VAPID_PUBLIC_KEY/VAPID_PRIVATE_KEY/VAPID_SUBJECT not set)")
		return nil
	}
	v, err := push.ParseVAPID(pubKey, privKey)
	if err != nil {
		log.Warn("web push disabled: invalid VAPID keys", "err", err)
		return nil
	}
	var st push.Store = push.NewMemoryStore()
	if pg != nil {
		st = &postgres.PushStore{S: pg}
	}
	hosts := append([]string{}, push.DefaultAllowedHosts...)
	for _, h := range strings.Split(os.Getenv("PUSH_ALLOWED_HOSTS"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	return push.New(push.Config{VAPID: v, Subject: subject, AllowedHosts: hosts, Log: log}, st)
}
