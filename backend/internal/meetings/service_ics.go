package meetings

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"aiworkforce/backend/internal/calendar"
)

// ICS export of one meeting and the per-person subscription feed.
//
// A feed URL is a capability (calendar apps cannot send a bearer token), so the token is
// long and random, shown once, stored only as a SHA-256, scoped to one person in one
// organization, and revoking it kills the URL at once. The feed lists the meetings that
// person organizes or attends (agents' preparation notes are not included), from 30 days
// ago to 180 days ahead, cancelled ones marked STATUS:CANCELLED so calendar apps drop them.

const (
	feedPast   = 30 * 24 * time.Hour
	feedFuture = 180 * 24 * time.Hour
	maxFeeds   = 10
	icsDomain  = "meetings.aiworkforce"
)

func icsEvent(m Meeting, now time.Time) calendar.ICSEvent {
	var att []calendar.ICSAttendee
	var names []string
	for _, a := range m.Attendees {
		names = append(names, a.Name)
		if a.Email != "" {
			att = append(att, calendar.ICSAttendee{Name: a.Name, Email: a.Email})
		}
	}
	desc := m.Description
	if len(m.Agenda) > 0 {
		desc = strings.TrimSpace(desc + "\n\n" + fmtAgenda(m.Agenda, "", "en"))
	}
	if len(names) > 0 {
		desc = strings.TrimSpace(desc + "\n\nParticipants: " + strings.Join(names, ", "))
	}
	seq := 0
	if m.UpdatedAt.After(m.CreatedAt) {
		seq = int(m.UpdatedAt.Sub(m.CreatedAt) / time.Minute) // grows with every edit: clients accept the newest copy
	}
	return calendar.ICSEvent{UID: m.ID + "@" + icsDomain, Title: m.Title, Description: desc, Location: m.Room, URL: m.JoinURL,
		Start: m.Start, End: m.End, Attendees: att, Cancelled: m.Status == StatusCancelled, Sequence: seq, Updated: m.UpdatedAt}
}

// ExportICS renders one meeting as a .ics file.
func (s *Service) ExportICS(ctx context.Context, org, id string) (string, string, error) {
	m, err := s.Store.GetMeeting(ctx, org, id)
	if err != nil {
		return "", "", err
	}
	return calendar.BuildICS(m.Title, []calendar.ICSEvent{icsEvent(m, s.Now().UTC())}, s.Now().UTC()), "meeting-" + roomSlug(m.Title) + ".ics", nil
}

func hashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// CreateFeed makes a subscription for a person. The returned token is the only time it is visible.
func (s *Service) CreateFeed(ctx context.Context, org, user, label string) (token string, f Feed, err error) {
	if strings.TrimSpace(user) == "" {
		return "", Feed{}, invalid("a feed belongs to a person")
	}
	existing, err := s.Store.ListFeeds(ctx, org, user)
	if err != nil {
		return "", Feed{}, err
	}
	if len(existing) >= maxFeeds {
		return "", Feed{}, invalid("at most %d active feeds per person: revoke one first", maxFeeds)
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", Feed{}, err
	}
	secret := base64.RawURLEncoding.EncodeToString(raw[:])
	f = Feed{ID: "cf_" + strings.ReplaceAll(newID(), "mt_", ""), OrgID: org, UserID: user, Label: oneLine(label, 80), TokenHash: hashSecret(secret), CreatedAt: s.Now().UTC()}
	if err := s.Store.CreateFeed(ctx, f); err != nil {
		return "", Feed{}, err
	}
	s.Audit(ctx, org, user, "calendar.feed_created", "calendar_feed", f.ID, map[string]any{"label": f.Label})
	return org + "." + secret, f, nil
}

// ListFeeds lists the active feeds of a person (never the token).
func (s *Service) ListFeeds(ctx context.Context, org, user string) ([]Feed, error) {
	fs, err := s.Store.ListFeeds(ctx, org, user)
	if fs == nil {
		fs = []Feed{}
	}
	return fs, err
}

// RevokeFeed kills a feed URL.
func (s *Service) RevokeFeed(ctx context.Context, org, user, id string) error {
	if err := s.Store.RevokeFeed(ctx, org, user, id, s.Now().UTC()); err != nil {
		return err
	}
	s.Audit(ctx, org, user, "calendar.feed_revoked", "calendar_feed", id, nil)
	return nil
}

// ParseFeedToken splits "<org>.<secret>" (the secret has no dots).
func ParseFeedToken(token string) (org, secret string, ok bool) {
	i := strings.LastIndex(token, ".")
	if i <= 0 || i == len(token)-1 || len(token) > 300 {
		return "", "", false
	}
	return token[:i], token[i+1:], true
}

// FeedICS serves the subscription: the token resolves the organization and the person.
func (s *Service) FeedICS(ctx context.Context, token string) (string, error) {
	org, secret, ok := ParseFeedToken(token)
	if !ok {
		return "", ErrNotFound
	}
	f, err := s.Store.FeedByHash(ctx, org, hashSecret(secret))
	if err != nil {
		return "", ErrNotFound
	}
	now := s.Now().UTC()
	ms, err := s.Store.ListMeetings(ctx, org, Filter{From: now.Add(-feedPast), To: now.Add(feedFuture), Person: f.UserID, Limit: 500})
	if err != nil {
		return "", err
	}
	evs := make([]calendar.ICSEvent, 0, len(ms))
	for _, m := range ms {
		if m.Status == StatusCancelled && m.CancelledAt != nil && now.Sub(*m.CancelledAt) > 7*24*time.Hour {
			continue
		}
		evs = append(evs, icsEvent(m, now))
	}
	name := "Meetings"
	if f.Label != "" {
		name += " - " + f.Label
	}
	return calendar.BuildICS(name, evs, now), nil
}

// Window validates a list window.
func Window(from, to time.Time) error {
	if !from.IsZero() && !to.IsZero() && !to.After(from) {
		return fmt.Errorf("%w: to must be after from", ErrInvalid)
	}
	return nil
}
