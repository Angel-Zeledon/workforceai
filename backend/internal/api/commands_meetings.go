package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"aiworkforce/backend/internal/drive"
	"aiworkforce/backend/internal/meetings"
)

// Meeting commands (docs/architecture/meetings.md): /room opens a meeting now in a free room,
// /schedule opens the schedule dialog prefilled, /minutes writes the minutes of the latest meeting
// from the notes you type (or a drive file). /meeting (agents discussing a topic) is unchanged.

func init() {
	avail := func(s *server) bool { return s.Meetings != nil }
	cmdHandlers["room"] = handler{run: cmdRoom, available: avail}
	cmdHandlers["schedule"] = handler{run: cmdSchedule, available: avail}
	cmdHandlers["minutes"] = handler{run: cmdMinutes, available: avail}
}

func (c *cmdCall) meetingFail(err error) *cmdFail {
	switch {
	case errors.Is(err, meetings.ErrRoomBusy):
		return fail(http.StatusConflict, "conflict", "Esa sala está ocupada a esa hora.", "That room is taken at that time.")
	case errors.Is(err, meetings.ErrState):
		return fail(http.StatusConflict, "conflict", "La reunión no está en un estado que permita eso.", "The meeting is not in a state that allows that.")
	case errors.Is(err, meetings.ErrInvalid):
		return fail(http.StatusBadRequest, "invalid_args", "Los datos de la reunión no son válidos.", "The meeting data is not valid.")
	case errors.Is(err, meetings.ErrNothingToDo):
		return fail(http.StatusUnprocessableEntity, "nothing_to_do", "No hay nada que hacer.", "There is nothing to do.")
	case errors.Is(err, meetings.ErrUnavailable):
		return fail(http.StatusNotImplemented, "not_available", "Esta función no está configurada.", "This feature is not configured.")
	case errors.Is(err, meetings.ErrBudget):
		return fail(http.StatusConflict, "budget_exceeded", "Se agotó el presupuesto para llamar al modelo.", "The budget for calling the model is used up.")
	}
	return c.serviceFail(err)
}

// agentIDs resolves the agent references of the line (ids, names or roles).
func (c *cmdCall) agentIDs() (ids, names []string, f *cmdFail) {
	for _, ref := range c.list("agents") {
		a := c.agentRef(ref)
		if a == nil {
			return nil, nil, fail(http.StatusBadRequest, "unknown_agent", fmt.Sprintf("No encuentro al agente «%s».", ref), fmt.Sprintf("I cannot find the agent “%s”.", ref))
		}
		if !slices.Contains(ids, a.ID) {
			ids, names = append(ids, a.ID), append(names, a.Name)
		}
	}
	return ids, names, nil
}

// ---- /room ----

func cmdRoom(c *cmdCall) (*cmdResult, *cmdFail) {
	ids, names, f := c.agentIDs()
	if f != nil {
		return nil, f
	}
	topic := strings.TrimSpace(c.str("topic"))
	if topic == "" {
		return nil, fail(http.StatusBadRequest, "missing_arg", "Falta el tema de la reunión.", "The meeting topic is missing.")
	}
	var att []meetings.Attendee
	for _, id := range ids {
		att = append(att, meetings.Attendee{Kind: meetings.KindAgent, ID: id})
	}
	now := time.Now().UTC()
	end := now.Add(time.Hour)
	room := ""
	if set, err := c.s.Meetings.EffectiveSettings(c.ctx, c.org); err == nil {
		busy, _ := c.s.Meetings.List(c.ctx, c.org, meetings.Filter{From: now, To: end, Status: []meetings.Status{meetings.StatusScheduled, meetings.StatusLive}})
		taken := map[string]bool{}
		for _, m := range busy {
			taken[m.Room] = true
		}
		for _, r := range set.Rooms {
			if !taken[r.ID] && (r.Capacity == 0 || r.Capacity > len(att)) {
				room = r.ID
				break
			}
		}
	}
	m, err := c.s.Meetings.Create(c.ctx, c.org, c.actor, meetings.CreateInput{Title: topic, Room: room, Start: now, End: end, Attendees: att})
	if err != nil {
		return nil, c.meetingFail(err)
	}
	if m, err = c.s.Meetings.Start(c.ctx, c.org, c.actor, m.ID, false, c.lang); err != nil {
		return nil, c.meetingFail(err)
	}
	text := c.t("La sala está abierta; entra con el enlace de video.", "The room is open; join with the video link.")
	if len(names) > 0 {
		text = fmt.Sprintf(c.t("Sala abierta con %s. Entra con el enlace de video.", "Room open with %s. Join with the video link."), strings.Join(names, ", "))
	}
	res := navResult("/meetings?open="+url.QueryEscape(m.ID), c.t("Reunión abierta: ", "Meeting open: ")+m.Title, text)
	res.Data = map[string]any{"meeting_id": m.ID, "room": m.Room, "join_url": m.JoinURL}
	return res, nil
}

// ---- /schedule ----

func cmdSchedule(c *cmdCall) (*cmdResult, *cmdFail) {
	ids, _, f := c.agentIDs()
	if f != nil {
		return nil, f
	}
	q := url.Values{"new": {"1"}}
	if t := strings.TrimSpace(c.str("topic")); t != "" {
		q.Set("title", t)
	}
	if len(ids) > 0 {
		q.Set("agents", strings.Join(ids, ","))
	}
	return navResult("/meetings?"+q.Encode(), c.t("Agendar una reunión", "Schedule a meeting"), ""), nil
}

// ---- /minutes ----

func cmdMinutes(c *cmdCall) (*cmdResult, *cmdFail) {
	notes := strings.TrimSpace(c.str("notes"))
	if notes == "" {
		return nil, fail(http.StatusBadRequest, "missing_arg", "Pega las notas o la ruta de un archivo del drive.", "Paste the notes or the path of a drive file.")
	}
	if !c.s.has(c.r, "files:write") {
		return nil, fail(http.StatusForbidden, "forbidden", "El acta se guarda en el drive: tu rol no puede escribir archivos.", "The minutes are saved in the drive: your role cannot write files.")
	}
	source := "notes"
	if strings.HasPrefix(notes, "/") && !strings.ContainsAny(notes, " \t\n") && c.s.Drive != nil && c.s.has(c.r, "files:read") {
		p, err := drive.CleanPath(notes)
		if err != nil {
			return nil, c.driveErr(err)
		}
		file, err := c.s.Drive.FileByPath(c.ctx, p)
		if err != nil {
			return nil, c.driveErr(err)
		}
		txt, err := c.s.Drive.ReadText(c.ctx, file, 0, 0, meetings.MaxNotesChars)
		if err != nil {
			return nil, c.driveErr(err)
		}
		notes, source = txt.Text, "upload"
	}
	ms, err := c.s.Meetings.List(c.ctx, c.org, meetings.Filter{Status: []meetings.Status{meetings.StatusLive, meetings.StatusEnded}, Limit: 200})
	if err != nil {
		return nil, c.meetingFail(err)
	}
	var target *meetings.Meeting
	now := time.Now().UTC()
	for i := len(ms) - 1; i >= 0; i-- { // newest first, only meetings that already began
		if ms[i].Start.After(now.Add(15 * time.Minute)) {
			continue
		}
		if target == nil {
			target = &ms[i]
		}
		if !ms[i].HasMinutes {
			target = &ms[i]
			break
		}
	}
	if target == nil {
		return nil, fail(http.StatusUnprocessableEntity, "nothing_to_do", "No hay ninguna reunión en curso o terminada para hacer el acta.", "There is no live or finished meeting to write minutes for.")
	}
	min, err := c.s.Meetings.GenerateMinutes(c.ctx, c.org, c.actor, target.ID, meetings.MinutesInput{Notes: notes, Source: source, Lang: c.lang})
	if err != nil {
		return nil, c.meetingFail(err)
	}
	text := fmt.Sprintf(c.t("Acta de «%s»: %d decisiones y %d acciones.", "Minutes of “%s”: %d decisions and %d action items."), target.Title, len(min.Decisions), len(min.ActionItems))
	if min.FilePath != "" {
		text += c.t(" Guardada en ", " Saved in ") + min.FilePath + "."
	}
	text += c.t(" Ábrela para revisar las acciones y crear las tareas.", " Open it to review the action items and create the tasks.")
	res := msgResult(c.t("Acta lista", "Minutes ready"), text, map[string]any{"meeting_id": target.ID, "href": "/meetings?open=" + url.QueryEscape(target.ID) + "&tab=minutes",
		"decisions": len(min.Decisions), "action_items": len(min.ActionItems), "file_path": min.FilePath, "summary_source": min.SummarySource})
	return res, nil
}
